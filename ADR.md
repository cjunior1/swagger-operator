# ADR-001: Criação de um Operador Kubernetes para Gerir Rotas do Kong a partir de Especificações Swagger
Status: Aceite

Data: 2025-08-14

## Contexto
A nossa plataforma, baseada em microserviços no EKS, utiliza o Kong como API Gateway. Atualmente, a configuração das rotas do Kong para cada novo serviço ou atualização de endpoint é um processo manual. Este processo envolve a criação ou modificação de recursos Ingress ou KongIngress por um engenheiro de plataforma, com base na documentação fornecida pela equipa de desenvolvimento.

Este fluxo de trabalho apresenta vários problemas:

Processo Lento e Propenso a Erros: A configuração manual está sujeita a erros de digitação e desalinhamentos entre a documentação e a implementação real da API.

Falta de Sincronização: Quando os desenvolvedores alteram um endpoint (mudam um caminho, adicionam um método HTTP), não há garantia de que a configuração do gateway seja atualizada em conformidade, levando a inconsistências e bugs.

Dependência da Equipa de Plataforma: As equipas de desenvolvimento dependem da equipa de plataforma para expor as suas APIs, criando um gargalo e reduzindo a sua autonomia.

Falta de uma "Fonte Única da Verdade": A configuração das rotas vive separada do código da aplicação, dificultando a rastreabilidade e a auditoria.

Precisamos de uma solução automatizada que trate a configuração do gateway como parte do ciclo de vida da aplicação, seguindo os princípios de GitOps e "API-as-code".

## Decisão
Decidimos projetar e implementar um Operador Kubernetes customizado, denominado swagger-kong-operator. Este operador irá automatizar a criação e gestão de rotas no Kong, usando a especificação OpenAPI/Swagger exposta pela própria aplicação como a fonte única da verdade.

A arquitetura da solução é a seguinte:

1. Introdução de um CRD SwaggerKong: Criamos um novo recurso no Kubernetes, SwaggerKong, que serve como o ponto de entrada para a automação. Cada aplicação terá um único recurso SwaggerKong associado.

2. Descoberta Dinâmica via HTTP: O operador não depende de ficheiros estáticos. Ele descobre a especificação OpenAPI fazendo um pedido HTTP diretamente a um endpoint exposto pelo próprio microserviço (ex: /swagger/doc.json).

3. Gestão de Rollouts: Para garantir que as rotas são criadas com base na versão correta da API, o operador monitoriza o Deployment associado. Ele só age quando o rollout do deployment está 100% completo e estável.

4. Deteção de Alterações por Checksum: Para evitar reconciliações desnecessárias e para reagir a alterações na API (mesmo que o SwaggerKong não mude), o operador calcula um checksum (SHA256) do conteúdo do Swagger. Ele só atualiza as rotas se o checksum mudar, guardando o último checksum aplicado no status do recurso SwaggerKong.

5. Geração de Ingress Padrão: O operador traduz cada caminho da especificação Swagger num recurso Ingress padrão do Kubernetes (networking.k8s.io/v1). Cada Ingress é configurado com anotações específicas do Kong (konghq.com/methods, konghq.com/strip-path, etc.) para garantir a configuração granular de cada rota.

6. Isolamento por Host a partir do Swagger: O host (domínio) para o qual as rotas são criadas é extraído diretamente do campo host ou servers da especificação Swagger, garantindo que a própria API define o seu endereço público e que não há conflitos de rotas entre diferentes aplicações.

## Consequências
### Positivas
- Automação Completa (GitOps): O código da API e a sua documentação godoc tornam-se a fonte única da verdade para o roteamento. Uma alteração no código aciona um novo build, um novo rollout, e o operador atualiza automaticamente o gateway.

- Redução Drástica de Erros: Elimina a possibilidade de erros manuais na configuração do Kong.

- Consistência Garantida: A configuração do gateway estará sempre sincronizada com a implementação real da API.

- Autonomia para Desenvolvedores: As equipas de desenvolvimento ganham total controlo sobre a exposição das suas APIs, simplesmente ao documentar o seu código. Isto acelera o ciclo de desenvolvimento.

- Padrão de Escalabilidade: A abordagem de isolamento por host cria um padrão claro e seguro para a integração de dezenas ou centenas de novos serviços no cluster.

### Negativas
- Aumento da Complexidade da Plataforma: Introduzimos um novo componente customizado (o operador) no cluster. Este componente precisa de ser mantido, monitorizado e atualizado pela equipa de plataforma.

- Dependência de Convenções: O sucesso desta abordagem depende da adesão de todas as equipas de desenvolvimento à convenção de documentar as suas APIs com swaggo e expor o endpoint /swagger/doc.json.

- Curva de Aprendizagem: Os engenheiros (tanto de plataforma como de produto) precisam de entender como este novo sistema funciona para poderem depurar problemas de roteamento.

- Potencial Ponto Único de Falha: Um bug ou uma falha no swagger-kong-operator poderia, teoricamente, impactar a configuração de rotas de todos os serviços que dependem dele. Requer monitorização e testes robustos.