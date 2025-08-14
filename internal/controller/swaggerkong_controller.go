/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/getkin/kin-openapi/openapi3"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kongv1alpha1 "swagger-kong-operator/api/v1"
)

// SwaggerKongReconciler reconciles a SwaggerKong object
type SwaggerKongReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=kong.swagger.com,resources=swaggerkongs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kong.swagger.com,resources=swaggerkongs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kong.swagger.com,resources=swaggerkongs/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kong.swagger.com,resources=swaggerkongs,verbs=get;list;watch;create;update;patch;delete

func (r *SwaggerKongReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var swaggerKong kongv1alpha1.SwaggerKong
	if err := r.Get(ctx, req.NamespacedName, &swaggerKong); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Recurso SwaggerKong não encontrado. Ignorando.")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Falha ao buscar SwaggerKong")
		return ctrl.Result{}, err
	}

	// consulta Deployment da aplicacao
	var deployment appsv1.Deployment
	deploymentName := types.NamespacedName{
		Namespace: swaggerKong.Namespace,
		Name:      swaggerKong.Spec.BackendService.Name,
	}
	if err := r.Get(ctx, deploymentName, &deployment); err != nil {
		logger.Error(err, "Falha ao buscar o deployment do backend", "Deployment", deploymentName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	//
	desiredReplicas := int32(1) // Defina o número desejado de réplicas
	if deployment.Spec.Replicas != nil {
		desiredReplicas = *deployment.Spec.Replicas
	}

	isStable := deployment.Status.ObservedGeneration >= deployment.Generation &&
		deployment.Status.UpdatedReplicas == desiredReplicas &&
		deployment.Status.AvailableReplicas == desiredReplicas &&
		deployment.Status.Replicas == desiredReplicas

	if !isStable {
		logger.Info("Deployment do backend ainda não está estável. Aguardando...", "Deployment", deploymentName)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	logger.Info("Deployment do backend está estável", "Deployment", deploymentName)

	swaggerEndpoint := swaggerKong.Spec.BackendService.SwaggerEndpoint
	if swaggerEndpoint == "" {
		swaggerEndpoint = "/swagger/doc.json"
	}

	serviceURL := fmt.Sprintf("http://%s.%s.svc:%d%s",
		swaggerKong.Spec.BackendService.Name,
		swaggerKong.Namespace,
		swaggerKong.Spec.BackendService.Port,
		swaggerEndpoint,
	)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(serviceURL)
	if err != nil {
		logger.Error(err, "Falha ao fazer pedido HTTP para o endpoint do Swagger", "URL", serviceURL)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("endpoint do Swagger retornou um status não-OK: %d", resp.StatusCode)
		logger.Error(err, "O serviço de backend não está a servir a especificação corretamente.")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	swaggerContent, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(err, "Falha ao ler o corpo da resposta do Swagger.")
		return ctrl.Result{}, err
	}

	// ** INÍCIO DA NOVA LÓGICA DE CHECKSUM **
	hasher := sha256.New()
	hasher.Write(swaggerContent)
	newChecksum := hex.EncodeToString(hasher.Sum(nil))

	if newChecksum == swaggerKong.Status.LastAppliedSwaggerChecksum {
		logger.Info("Checksum do Swagger não mudou. Nenhuma ação necessária.")
		return ctrl.Result{}, nil
	}
	logger.Info("Checksum do Swagger mudou. A reconciliar rotas.", "Checksum Antigo", swaggerKong.Status.LastAppliedSwaggerChecksum, "Checksum Novo", newChecksum)
	// ** FIM DA NOVA LÓGICA DE CHECKSUM **

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData([]byte(swaggerContent))
	if err != nil {
		logger.Error(err, "Falha ao analisar o documento Swagger/OpenAPI")
		return ctrl.Result{}, nil
	}

	routeCount := 0
	for path, pathItem := range doc.Paths.Map() {
		// Agrupar todos os métodos para um único caminho
		var methods []string
		for method := range pathItem.Operations() {
			methods = append(methods, strings.ToUpper(method))
		}

		path = "/" + swaggerKong.Spec.BackendService.Name + path

		sanitizedPath := strings.Trim(path, "/")
		sanitizedPath = strings.ReplaceAll(sanitizedPath, "/", "-")
		sanitizedPath = strings.ReplaceAll(sanitizedPath, "{", "")
		sanitizedPath = strings.ReplaceAll(sanitizedPath, "}", "")

		ingressName := fmt.Sprintf("%s-%s",
			swaggerKong.Name,
			strings.ToLower(sanitizedPath),
		)

		// Objeto Ingress padrão do Kubernetes
		desiredIngress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ingressName,
				Namespace: swaggerKong.Namespace,
			},
		}

		result, err := ctrl.CreateOrUpdate(ctx, r.Client, desiredIngress, func() error {
			// Anotações para configurar o comportamento do Kong
			annotations := map[string]string{
				"konghq.com/strip-path": "true",
				"konghq.com/methods":    strings.Join(methods, ","),
			}

			processedPath := path
			pathType := networkingv1.PathTypeExact

			if strings.Contains(path, "{") {
				// Converte /path/{param} para uma regex válida como /~/path/([^/]+)
				// que passa na validação do Kubernetes e é entendida pelo Kong.
				re := regexp.MustCompile(`\{[^}]+\}`)
				// Substitui {param} pelo grupo de captura regex
				regexPath := re.ReplaceAllString(path, `([^/]+)`)
				// Remove a barra inicial para evitar caminhos como /~/path
				regexPath = strings.TrimPrefix(regexPath, "/")
				// Adiciona o prefixo /~ que o Kong usa para identificar regex
				processedPath = "/~/" + regexPath

				pathType = networkingv1.PathTypeImplementationSpecific

				// Adiciona a anotação de prioridade para rotas regex, uma boa prática do Kong
				annotations["konghq.com/regex-priority"] = "1"
			}

			desiredIngress.Annotations = annotations

			logger.Info("PATH= " + processedPath + " and PATH_TYPE= " + string(pathType))

			//pathType := networkingv1.PathTypePrefix
			backend := networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: swaggerKong.Spec.BackendService.Name,
					Port: networkingv1.ServiceBackendPort{
						Number: int32(swaggerKong.Spec.BackendService.Port),
					},
				},
			}

			// Definir as regras do Ingress
			desiredIngress.Spec.Rules = []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     processedPath,
									PathType: &pathType,
									Backend:  backend,
								},
							},
						},
					},
				},
			}
			// Definir a classe de Ingress para garantir que o Kong o processe
			ingressClassName := "kong"
			desiredIngress.Spec.IngressClassName = &ingressClassName

			// Definir o SwaggerKong como dono para garbage collection
			return ctrl.SetControllerReference(&swaggerKong, desiredIngress, r.Scheme)
		})

		if err != nil {
			logger.Error(err, "Falha ao criar ou atualizar Ingress", "Ingress", ingressName)
			return ctrl.Result{}, err
		}

		if result != controllerutil.OperationResultNone {
			logger.Info("Ingress reconciliado", "Ingress", ingressName, "Resultado", result)
		}
		routeCount++
	}

	swaggerKong.Status.RouteCount = routeCount
	swaggerKong.Status.LastAppliedSwaggerChecksum = newChecksum
	if err := r.Status().Update(ctx, &swaggerKong); err != nil {
		logger.Error(err, "Falha ao atualizar o status do SwaggerKong")
		return ctrl.Result{}, err
	}

	logger.Info("Reconciliação do SwaggerKong concluída com sucesso.", "Nome", swaggerKong.Name, "Ingresses Criados/Atualizados", routeCount)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SwaggerKongReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kongv1alpha1.SwaggerKong{}).
		// Observar os Ingresses que nós criamos
		Owns(&networkingv1.Ingress{}).
		// Observar os ConfigMaps para reagir a mudanças no Swagger
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
