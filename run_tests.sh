#!/bin/bash


echo "Ejecutando tests unitarios del backend de walkie-talkie..."

echo "Verificando dependencias de test..."
go mod tidy

go get github.com/DATA-DOG/go-sqlmock

echo "Ejecutando tests con coverage..."
go test -v -cover ./...

echo "Ejecutando tests con race detection..."
go test -race -v ./...

echo "Tests completados!"

echo "Generando reporte de coverage..."
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html

echo "Reporte de coverage generado: coverage.html"
echo "¡Todos los tests han sido ejecutados!"
