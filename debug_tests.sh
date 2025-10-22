#!/bin/bash

echo "🔍 Ejecutando tests individuales para identificar errores..."

# Instalar dependencias necesarias
echo "📦 Instalando dependencias..."
go get github.com/DATA-DOG/go-sqlmock

# Ejecutar tests de modelos
echo "🧪 Testing models..."
go test ./internal/models/... -v

# Ejecutar tests de servicios
echo "🧪 Testing services..."
go test ./internal/services/... -v

# Ejecutar tests de handlers
echo "🧪 Testing handlers..."
go test ./internal/http/handlers/... -v

# Ejecutar tests de paquetes externos
echo "🧪 Testing external packages..."
go test ./pkg/... -v
