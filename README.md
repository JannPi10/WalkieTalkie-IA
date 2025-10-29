# Walkie-Backend-IA

Un backend de IA avanzado para walkie-talkies, diseñado para mejorar la accesibilidad de personas con discapacidad visual y motriz. Permite comunicación por voz en canales, con análisis inteligente de comandos para navegación intuitiva.

## Descripción

Este proyecto implementa un servidor backend que procesa audio en tiempo real, lo transcribe a texto usando modelos de IA locales, analiza el contenido para detectar comandos o conversaciones, y facilita la comunicación entre usuarios vía WebSockets. El enfoque principal es la accesibilidad, permitiendo que personas con discapacidades visuales o motrices interactúen con un walkie-talkie usando comandos de voz simples, como "conectar al canal 1" o "lista de canales", sin necesidad de interfaces gráficas complejas.

### Finalidad y Accesibilidad
- **Accesibilidad Visual**: Los usuarios ciegos pueden recibir feedback auditivo y controlar el sistema por voz.
- **Accesibilidad Motriz**: Comandos de voz eliminan la necesidad de interacción táctil o manual.
- **Comandos Intuitivos**: Frases naturales como "tráeme la lista de canales" o "salir del canal" permiten navegación fácil.
- **Comunicación Inclusiva**: Facilita la participación en conversaciones grupales sin barreras.

## Características
- **Transcripción de Audio (STT)**: Convierte voz a texto usando modelos locales de Whisper.
- **Análisis de IA**: Detecta comandos vs. conversaciones usando modelos de lenguaje como Qwen.
- **Gestión de Canales**: Conexión/desconexión a canales por voz.
- **Comunicación en Tiempo Real**: WebSockets para broadcasting de audio.
- **Base de Datos**: Persistencia de usuarios, canales y membresías con PostgreSQL.
- **Arquitectura Local**: Todo corre en Docker, sin dependencias externas de internet.
- **Cobertura de Tests**: Más del 70% de cobertura para asegurar estabilidad.

## Tecnologías Usadas
- **Go**: Lenguaje principal para el backend, con handlers HTTP y WebSockets.
- **PostgreSQL**: Base de datos para usuarios y canales.
- **Speaches AI**: STT basado en Assembly para transcripción.
- **Gorilla WebSockets**: Para conexiones en tiempo real.
- **GORM**: ORM para Go y PostgreSQL.
- **Docker Compose**: Orquestación de contenedores.
- **Bcrypt**: Encriptación de pines de usuarios.

## Requisitos
- Docker y Docker Compose (versión 2.0+).
- Puerto 80 disponible (o ajustar en docker-compose.yml).
- Puerto 5432 para PostgreSQL (opcional, si expuesto).

## Instalación y Ejecución

### 1. Clonar el Repositorio
```bash
git clone https://github.com/JannPi10/WalkieTalkie-IA.git
```

### 2. Configurar Variables de Entorno
Crea un archivo `.env` basado en `.env.example`:
```
PORT=8080
DATABASE_URL=postgres://tuUsuario:tuContraseña@db:5432/walkie_db?sslmode=disable
AI_API_URL=http://tuModeloDeIA....
ASSEMBLYAI_API_KEY=686877......
```

### 3. Construir y Ejecutar con Docker
```bash
docker-compose up --build
```
Esto iniciará:
- PostgreSQL (db)
- El backend de Go (app)

El servidor estará disponible en `http://localhost:80`.

### 4. Verificar Modelos
Los contenedores verifican automáticamente la disponibilidad de modelos. Si falla, revisa logs con `docker-compose logs`.

## Uso

### Autenticación
Regístrate o inicia sesión enviando POST a `/auth`:
```bash
curl -X POST http://localhost:80/auth \
  -H "Content-Type: application/json" \
  -d '{"nombre":"Juan","pin":1234}'
```
Respuesta: `{"message":"usuario registrado exitosamente","token":"..."}`

### Enviar Audio
Envía audio WAV a `/audio/ingest` con el token:
```bash
curl -X POST http://localhost:80/audio/ingest \
  -H "Content-Type: audio/wav" \
  -H "X-Auth-Token: TU_TOKEN" \
  --data-binary @sample.wav
```

### Comandos de Voz Ejemplos
- "Tráeme la lista de canales"
- "Conectar al canal 1"
- "Salir del canal"
- Conversaciones libres: Cualquier frase no reconocida como comando se trata como conversación.

### WebSocket
Conecta a `/ws` para recibir audio en tiempo real.

## Tests
Ejecuta tests con cobertura:
```bash
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
```
Abre `coverage.html` para ver reporte visual.

## Contribución
1. Fork el repo.
2. Crea una rama: `git checkout -b feature/nueva-funcionalidad`.
3. Commitea cambios: `git commit -m 'Agrega nueva funcionalidad'`.
4. Push: `git push origin feature/nueva-funcionalidad`.
5. Abre un Pull Request.

## Contacto
Para preguntas o soporte: [jann.ortiz@gopenux.com](mailto:tu-email@example.com)
