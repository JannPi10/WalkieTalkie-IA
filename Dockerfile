FROM golang:1.24.5-bullseye

# Instalar curl para verificaciones
RUN apt-get update && apt-get install -y curl && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o main ./cmd/server

# Hacer ejecutable el script de verificación
RUN chmod +x scripts/verify-models.sh

EXPOSE 8080

# Ejecutar verificación de modelos antes de iniciar la aplicación
CMD ["sh", "-c", "./scripts/verify-models.sh && ./main"]
