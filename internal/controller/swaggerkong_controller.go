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
	"os"
	"regexp"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-openapi/loads"
	"github.com/go-openapi/spec"
	"github.com/oasdiff/yaml"

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

	// valida se houve mudanças no conteúdo do Swagger
	hasher := sha256.New()
	hasher.Write(swaggerContent)
	newChecksum := hex.EncodeToString(hasher.Sum(nil))

	if newChecksum == swaggerKong.Status.LastAppliedSwaggerChecksum {
		logger.Info("Checksum do Swagger não mudou. Nenhuma ação necessária.")
		return ctrl.Result{}, nil
	}
	logger.Info("Checksum do Swagger mudou. A reconciliar rotas.", "Checksum Antigo", swaggerKong.Status.LastAppliedSwaggerChecksum, "Checksum Novo", newChecksum)

	// Converte o YAML em []byte para JSON em []byte.
	// A função loads.Analyzed espera JSON.
	swaggerJSONBytes, err := yaml.YAMLToJSON(swaggerContent)
	if err != nil {
		fmt.Printf("Erro ao converter YAML para JSON: %v\n", err)
		os.Exit(1)
	}

	specDoc, err := loads.Analyzed(swaggerJSONBytes, "2.0")
	if err != nil {
		logger.Error(err, "Falha ao carregar a especificação Swagger/OpenAPI")
		return ctrl.Result{}, nil

	}

	if err := spec.ExpandSpec(specDoc.Spec(), nil); err != nil {
		logger.Error(err, "Especificação Swagger/OpenAPI inválida")
		return ctrl.Result{}, nil
	}

	// TODO: Validar regras de governança internas, como:

	// fim da analise do Swagger/OpenAPI

	// criar ingress para todas as rotas do Swagger
	ingressName := fmt.Sprintf("%s-ingress", deployment.Name)

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
			"konghq.com/preserve-host":  "true",
			"konghq.com/regex-priority": "1",
		}

		//processedPath := "/"

		desiredIngress.Annotations = annotations

		backend := networkingv1.IngressBackend{
			Service: &networkingv1.IngressServiceBackend{
				Name: swaggerKong.Spec.BackendService.Name,
				Port: networkingv1.ServiceBackendPort{
					Number: int32(swaggerKong.Spec.BackendService.Port),
				},
			},
		}

		var routes []networkingv1.HTTPIngressPath

		for path := range specDoc.Spec().Paths.Paths {
			processedPath := path
			pathType := networkingv1.PathTypeExact

			if strings.Contains(path, "{") {
				re := regexp.MustCompile(`\{[^}]+\}`)
				regexPath := re.ReplaceAllString(path, `([^/]+)`)
				regexPath = strings.TrimPrefix(regexPath, "/")
				processedPath = "/~/" + regexPath
				pathType = networkingv1.PathTypeImplementationSpecific
			}

			routes = append(routes, networkingv1.HTTPIngressPath{
				Path:     processedPath,
				PathType: &pathType,
				Backend:  backend,
			},
			)
		}

		// Definir as regras do Ingress
		desiredIngress.Spec.Rules = []networkingv1.IngressRule{
			{
				Host: specDoc.Host(),
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: routes,
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

	swaggerKong.Status.LastAppliedSwaggerChecksum = newChecksum
	if err := r.Status().Update(ctx, &swaggerKong); err != nil {
		logger.Error(err, "Falha ao atualizar o status do SwaggerKong")
		return ctrl.Result{}, err
	}

	logger.Info("Reconciliação do SwaggerKong concluída com sucesso.", "Nome", swaggerKong.Name, "Ingresses Criados/Atualizados")
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
