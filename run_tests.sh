#!/bin/bash

# Script para ejecutar tests unitarios del proyecto backend-IA

echo "🚀 Ejecutando tests unitarios del backend de walkie-talkie..."

# Instalar dependencias de test si no están instaladas
echo "📦 Verificando dependencias de test..."
go mod tidy

# Instalar sqlmock si no está instalado
go get github.com/DATA-DOG/go-sqlmock

# Ejecutar tests con coverage
echo "🧪 Ejecutando tests con coverage..."
go test -v -cover ./...

# Ejecutar tests de manera más detallada
echo "📊 Ejecutando tests con race detection..."
go test -race -v ./...

echo "✅ Tests completados!"

# Mostrar resumen de coverage si está disponible
echo "📈 Generando reporte de coverage..."
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html

echo "📄 Reporte de coverage generado: coverage.html"
echo "🎉 ¡Todos los tests han sido ejecutados!"
