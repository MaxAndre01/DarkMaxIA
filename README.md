# DarkMax Bot

Bot de Telegram con IA para consultas de ciberseguridad, hacking, OSINT y programación.

## Características

- Autenticación por keys (user, vip, admin)
- Integración con OpenRouter (múltiples modelos)
- Soporte para chats privados y grupos
- Panel de administración
- Persistencia de sesiones y keys

## Requisitos

- Go 1.25.5 o superior
- Token de bot de Telegram (obtenido via [@BotFather](https://t.me/BotFather))
- API keys de OpenRouter (obtenidas en [OpenRouter](https://openrouter.ai))

## Configuración

1. Clona el repositorio:
   ```bash
   git clone git@github.com:MaxAndre01/Bot.git
   cd Bot
   ```

2. Copia el archivo de entorno de ejemplo y configúralo:
   ```bash
   cp .env.example .env
   ```

3. Edita el archivo `.env` con tus credenciales:
   - `TELEGRAM_BOT_TOKEN`: Token de tu bot de Telegram
   - `OPENROUTER_KEYS`: Lista de keys de OpenRouter separadas por comas
   - `ADMIN_KEY`: (Opcional) Key de administrador. Si no se define, se generará una automáticamente.

4. Compila el bot:
   ```bash
   go build -o darkmax
   ```

5. Ejecuta el bot:
   ```bash
   ./darkmax
   ```

## Uso

- Envía `/start` al bot en Telegram para iniciar.
- Introduce tu keymaster para autenticarte.
- Usa `/menu` para acceder al menú principal.
- Los administradores pueden usar `/admin` para el panel de administración.

## Comandos de administrador

- `/deletekey KEY` – Elimina una key
- `/keyinfo KEY` – Muestra información de una key
- `/setrank KEY user|vip|admin` – Cambia el rango de una key
- `/togglekey KEY` – Activa/desactiva una key
- `/listkeys` – Lista todas las keys
- `/sessions` – Lista sesiones activas
- `/changeadminkey NUEVA` – Cambia la key de administrador

## Estructura de archivos

- `darkmax.go` – Código fuente principal
- `keys.json` – Base de datos de keys y sesiones (se crea automáticamente)
- `.env` – Variables de entorno (no incluido en el repositorio)
- `.gitignore` – Archivos ignorados por git

## Seguridad

- Nunca compartas tu archivo `.env` o `keys.json`.
- Rota las keys de OpenRouter periódicamente.
- Usa una clave de administrador segura.
- Considera usar un firewall para restringir el acceso al bot.

## Licencia

Este proyecto es de código abierto. Consulta el archivo LICENSE para más detalles.