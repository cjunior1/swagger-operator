#!/bin/bash

# O comando 'set -e' garante que o script irá parar imediatamente
# se qualquer um dos comandos falhar. Isso evita, por exemplo,
# tentar fazer o deploy de uma imagem que não foi construída corretamente.
set -e

# --- 1. Validação do Parâmetro ---
# Verifica se o primeiro argumento (a versão) foi passado para o script.
# Se não foi, exibe uma mensagem de erro e como usar, e então sai.
if [ -z "$1" ]; then
  echo "❌ Erro: A tag da versão é obrigatória."
  echo "Uso: $0 <sua-versao>"
  # Sai com status 1 para indicar um erro.
  exit 1
fi


# --- 2. Definição de Variáveis ---
# Armazena o primeiro argumento em uma variável para clareza.
VERSION=$1
# Constrói o nome completo da imagem para evitar repetição.
IMAGE_NAME="cjunior1/swagger-kong-operator:${VERSION}"

echo "🚀 Iniciando o processo para a imagem: ${IMAGE_NAME}"
echo "--------------------------------------------------------"


# --- 3. Execução dos Comandos ---
# Cada passo é anunciado com um 'echo' para que o usuário saiba o que está acontecendo.

echo "➡️ (1/3) Construindo a imagem Docker..."
make docker-build IMG="${IMAGE_NAME}"
echo "✅ Imagem construída com sucesso."
echo "" # Linha em branco para espaçamento

echo "➡️ (2/3) Carregando a imagem no cluster Kind..."
kind load docker-image "${IMAGE_NAME}"
echo "✅ Imagem carregada no Kind com sucesso."
echo ""

echo "➡️ (3/3) Realizando o deploy da aplicação..."
make deploy IMG="${IMAGE_NAME}"
echo "✅ Deploy finalizado."


# --- 4. Mensagem Final ---
echo "--------------------------------------------------------"
echo "🎉 Processo de build e deploy concluído com sucesso!"
