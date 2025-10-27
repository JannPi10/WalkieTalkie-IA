FROM golang:1.24.5-bullseye

RUN apt-get update \
 && apt-get install -y --no-install-recommends python3 python3-pip curl \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN pip3 install --no-cache-dir assemblyai \
 && chmod +x scripts/assemblyai_transcribe.py \
 && go build -o main ./cmd/server

RUN chmod +x scripts/verify-models.sh

EXPOSE 8080

CMD ["sh", "-c", "./scripts/verify-models.sh && ./main"]