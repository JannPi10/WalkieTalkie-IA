#!/bin/bash

echo "Ejecutando tests individuales para identificar errores..."

echo "Instalando dependencias..."
go get github.com/DATA-DOG/go-sqlmock

echo "Testing models..."
go test ./internal/models/... -v

echo "Testing services..."
go test ./internal/services/... -v

echo "Testing handlers..."
go test ./internal/http/handlers/... -v

echo "Testing external packages..."
go test ./pkg/... -v
