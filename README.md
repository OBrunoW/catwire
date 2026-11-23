# catwire

Serviço de WhatsApp com API HTTP: gerencia instâncias (uma sessão por número), envia e recebe mensagens e mídia, guarda o histórico e publica eventos por Kafka e por webhook assinado.

## Aviso

O catwire usa a biblioteca [whatsmeow](https://github.com/tulir/whatsmeow), que é não oficial e não tem relação com o WhatsApp. Automatizar o WhatsApp pode levar ao bloqueio do número. O uso é por conta e risco de quem usa.

## Status

A v1 está em desenvolvimento. A lógica está coberta por testes automatizados, mas o pareamento e o envio com um celular real ainda dependem de verificação manual (veja [Desenvolvimento](#desenvolvimento)).

| Versão | Escopo |
|--------|--------|
| v1 | instâncias (QR e pair code), configurações, perfil e privacidade, checagem de número, envio de texto, imagem, áudio (nota de voz), vídeo e documento, recebimento, recibos, edições, revogações e reações, chats, histórico, mídia, webhooks, eventos por Kafka e webhook |
| v2 | grupos e agendamento de envio |
| v3 | listas de transmissão (apenas emuladas) e stories |

Na v1, mensagens de `status@broadcast` são ignoradas.

## Requisitos

- Go 1.26
- Postgres 14 ou superior
- ffmpeg, para converter áudio em nota de voz (a imagem Docker já inclui)
- Kafka, opcional: sem `KAFKA_BROKERS` o Kafka fica desligado e os eventos saem só por webhook

## Início rápido

Suba um Postgres, construa a imagem a partir do `Dockerfile` e rode o serviço. As tabelas são criadas na subida.

```bash
docker network create catwire

docker run -d --name catwire-db --network catwire \
  -e POSTGRES_USER=catwire -e POSTGRES_PASSWORD=catwire -e POSTGRES_DB=catwire \
  postgres:16

docker build -t catwire .

docker run -d --name catwire --network catwire -p 5001:5001 \
  -e APP_ENV=development \
  -e DATABASE_URL='postgres://catwire:catwire@catwire-db:5432/catwire?sslmode=disable' \
  -e SERVICE_KEYS='local:my-local-key' \
  -v catwire-media:/data/media \
  catwire
```

Crie uma instância, conecte e leia o QR (o `code` deve ser lido por um gerador de QR e escaneado em Aparelhos conectados no celular):

```bash
curl -s -X POST http://localhost:5001/api/v1/instances \
  -H 'X-Service-Key: my-local-key' -H 'Content-Type: application/json' \
  -d '{"external_id": "user-1", "name": "Comercial"}'

curl -s -X POST http://localhost:5001/api/v1/instances/$INSTANCE_ID/connect \
  -H 'X-Service-Key: my-local-key'
```

Com a instância em `connected`, envie uma mensagem. A resposta é `202` com `status = pending` e `wa_message_id`; o resultado final chega pelo evento `whatsapp.message.sent` ou `whatsapp.message.failed`.

```bash
curl -s -X POST http://localhost:5001/api/v1/instances/$INSTANCE_ID/messages \
  -H 'X-Service-Key: my-local-key' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: order-1001' \
  -d '{"to": "5511988887777", "type": "text", "text": "Olá"}'
```

`GET /health` e `GET /ready` não exigem chave. `/ready` informa o estado do banco e do Kafka.

## Configuração

Todas as variáveis ficam em `.env.example`. Valores numéricos precisam ser inteiros positivos.

| Variável | Padrão | Descrição |
|----------|--------|-----------|
| `APP_ENV` | `production` | `development` ou `production` |
| `PORT` | `5001` | porta HTTP |
| `DATABASE_URL` | obrigatória | conexão Postgres |
| `SERVICE_KEYS` | vazio | chaves de serviço no formato `nome:chave,nome:chave` |
| `API_KEY` | vazio | chave única para sistemas externos |
| `KAFKA_BROKERS` | vazio | brokers separados por vírgula; vazio desliga o Kafka |
| `PUBLIC_BASE_URL` | vazio | base pública das URLs assinadas de mídia |
| `MEDIA_DIR` | `/data/media` | diretório do storage local de mídia |
| `MEDIA_MAX_BYTES` | `104857600` | tamanho máximo de uma mídia, em bytes |
| `MEDIA_URL_SECRET` | vazio | segredo HMAC das URLs assinadas; mínimo de 32 caracteres quando `PUBLIC_BASE_URL` está definida |
| `MEDIA_URL_TTL_SECONDS` | `3600` | validade da URL assinada |
| `SEND_MAX_AGE_SECONDS` | `600` | tempo que uma mensagem `pending` espera antes de virar `failed` |
| `WEBHOOK_MAX_ATTEMPTS` | `10` | tentativas de entrega antes de a delivery virar `dead` |
| `WEBHOOK_TIMEOUT_SECONDS` | `10` | timeout de cada entrega de webhook |
| `OUTBOX_RETENTION_DAYS` | `7` | retenção de eventos despachados e deliveries antigas |
| `FFMPEG_PATH` | `ffmpeg` | caminho do binário do ffmpeg |

Os valores de `.env.example` (`changeme-...`) são só para desenvolvimento.

## Autenticação

Toda rota de `/api/v1` exige uma chave, exceto a leitura de conteúdo de mídia com URL assinada.

| Header | Configuração | Observação |
|--------|--------------|------------|
| `X-Service-Key` | `SERVICE_KEYS`, formato `nome:chave,nome:chave` | o `nome` identifica o cliente |
| `x-api-key` | `API_KEY` | chave única, cliente `api` |

- Sem nenhuma chave configurada, o processo só sobe com `APP_ENV=development`. Fora disso encerra com erro.
- Com `APP_ENV=production`, o processo recusa `SERVICE_KEYS`, `API_KEY` e `MEDIA_URL_SECRET` que comecem com `changeme`.
- Instâncias e webhooks pertencem ao cliente que os criou. Recurso de outro cliente responde `404`.

## API

Prefixo `/api/v1`, JSON em `snake_case`. Sucesso responde `{ "success": true, "data": ... }` e erro responde `{ "success": false, "error": { "code": "...", "message": "..." } }`. Listas aceitam `limit` (padrão 50, máximo 100) e `cursor`.

| Recurso | Rotas |
|---------|-------|
| Instâncias | `POST/GET /instances`, `GET/PATCH/DELETE /instances/{instance_id}`, `POST /instances/{instance_id}/connect`, `GET .../qrcode`, `POST .../pair-code`, `POST .../disconnect`, `POST .../logout` |
| Configurações | `GET/PATCH /instances/{instance_id}/settings`, `/profile` e `/privacy` |
| Contatos | `POST /instances/{instance_id}/contacts/check` |
| Mensagens | `POST/GET /instances/{instance_id}/messages`, `GET .../messages/{message_id}` |
| Chats | `GET /instances/{instance_id}/chats`, `GET .../chats/{chat_id}`, `POST .../chats/{chat_id}/read`, `POST .../chats/{chat_id}/presence` |
| Histórico | `POST /instances/{instance_id}/chats/{chat_id}/history` |
| Mídia | `POST /instances/{instance_id}/media`, `GET /media/{media_id}`, `GET /media/{media_id}/content` |
| Webhooks | `POST/GET /webhooks`, `GET/PATCH/DELETE /webhooks/{webhook_id}`, `POST .../rotate-secret`, `POST .../test`, `GET .../deliveries`, `POST .../deliveries/{delivery_id}/retry` |

O envio aceita `Idempotency-Key`: a mesma chave com o mesmo corpo devolve a mensagem existente, e com corpo diferente responde `409 IDEMPOTENCY_CONFLICT`.

## Eventos

Cada mudança relevante gera um evento gravado numa outbox, na mesma transação do dado, e depois publicado no Kafka e nos webhooks que o assinam. A entrega é pelo menos uma vez: o consumidor deve deduplicar por `event_id`.

| event_type | Quando |
|------------|--------|
| `whatsapp.instance.status_changed` | o status da instância mudou |
| `whatsapp.message.received` | mensagem recebida ao vivo |
| `whatsapp.message.sent` | mensagem enviada pela API ou vista vinda de outro dispositivo |
| `whatsapp.message.failed` | envio pela API falhou de vez |
| `whatsapp.message.status_updated` | recibo mudou o status da mensagem |
| `whatsapp.message.updated` | edição, revogação ou reação |
| `whatsapp.media.stored` | mídia recebida foi gravada |
| `whatsapp.media.failed` | download da mídia esgotou as tentativas |
| `whatsapp.history.synced` | um lote de histórico foi gravado |
| `webhook.test` | teste de webhook (só webhook, nunca Kafka) |

Envelope:

```json
{
  "event_id": "0193a1f3-0000-7000-8000-0000000000e1",
  "event_type": "whatsapp.message.received",
  "event_version": 1,
  "sequence": 128734,
  "occurred_at": "2026-11-22T13:05:00Z",
  "instance": { "id": "...", "external_id": "user-1", "phone": "5511999999999" },
  "data": {}
}
```

- `sequence` cresce na outbox e não é contíguo. Use `sequence` e `occurred_at` para ordenar.
- Kafka: o tópico é o `event_type`, a chave é o `partition_key` e o valor é o envelope. O `partition_key` é `<instance_id>:<chat_jid>` para eventos de mensagem e mídia, e `<instance_id>` para instância e histórico. Headers: `event_id`, `event_type`, `event_version`, `instance_id`. A ordem só é garantida por `partition_key` dentro de um tópico.
- Webhook: `POST` com o envelope e os headers `X-Catwire-Event-Id`, `X-Catwire-Event-Type`, `X-Catwire-Delivery-Id`, `X-Catwire-Timestamp` e `X-Catwire-Signature`. Qualquer `2xx` dentro de `WEBHOOK_TIMEOUT_SECONDS` conta como sucesso; redirecionamentos não são seguidos. O segredo do webhook aparece só na criação e em `rotate-secret`.
- Falhas são retentadas com backoff de 30 s, 2 min, 10 min, 30 min, 1 h, 2 h, 4 h, 8 h e 12 h. Ao atingir `WEBHOOK_MAX_ATTEMPTS`, a delivery vira `dead` e pode ser reenviada por `POST /webhooks/{webhook_id}/deliveries/{delivery_id}/retry`.

### Verificar a assinatura

`X-Catwire-Signature` vale `sha256=` seguido do hex de `HMAC-SHA256(secret, timestamp + "." + corpo)`, com o corpo bruto como foi recebido. Rejeite requisições com `X-Catwire-Timestamp` mais de 5 minutos distante do relógio local.

```python
import hashlib
import hmac
import time


def is_valid(secret: str, timestamp: str, body: bytes, signature: str) -> bool:
    if abs(time.time() - int(timestamp)) > 300:
        return False
    payload = timestamp.encode() + b"." + body
    expected = "sha256=" + hmac.new(secret.encode(), payload, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, signature)
```

## Mídia

- A mídia recebida é baixada logo após a chegada e gravada em `MEDIA_DIR` (storage local). O tamanho máximo é `MEDIA_MAX_BYTES`.
- O conteúdo sai em `GET /api/v1/media/{media_id}/content`, com header de autenticação ou com URL assinada.
- A URL assinada (`download_url` na mensagem) só é gerada com `MEDIA_URL_SECRET` e `PUBLIC_BASE_URL` definidos, e vale por `MEDIA_URL_TTL_SECONDS`.
- Para enviar mídia, informe `url`, `base64` ou o `media_id` de um upload feito em `POST /instances/{instance_id}/media`. Nota de voz exige ffmpeg.
- Os dados para baixar de novo a mídia (chaves e caminhos do WhatsApp) ficam no banco e não saem pela API.

## Arquitetura

| Camada | Pasta | Papel |
|--------|-------|-------|
| entrada | `cmd/catwire/` | configuração, montagem das dependências, servidor e workers |
| domínio | `internal/domain/` | entidades, erros tipados e interfaces |
| casos de uso | `internal/application/` | regras de instância, mensagem, mídia, histórico, webhook e eventos |
| adapters | `internal/adapters/` | `http`, `whatsapp`, `postgres`, `kafka`, `webhook`, `storage` e `migrator` |
| infra | `internal/config/`, `internal/middleware/` | variáveis de ambiente e autenticação |
| SQL | `migrations/` | schema `catwire`, aplicado na subida |

O handler HTTP só lê e responde, o caso de uso aplica a regra e o repositório executa SQL parametrizado, sem ORM. O adapter `whatsapp` só traduz eventos do whatsmeow em chamadas de caso de uso.

Mensagem recebida:

1. O adapter `whatsapp` recebe o evento e chama o caso de uso de ingestão.
2. Numa transação, grava o chat, a mensagem (sem duplicar) e o evento na outbox.
3. O media worker baixa a mídia, grava no storage e emite `media.stored` ou `media.failed`.
4. O relay lê a outbox em ordem, publica no Kafka e cria as deliveries dos webhooks assinantes.
5. O dispatcher entrega os webhooks, com assinatura e backoff.

Mensagem enviada:

1. `POST /messages` valida a instância, o tipo, o conteúdo e o destinatário, e grava a mensagem como `pending` com o `wa_message_id` já gerado.
2. O send worker da instância envia em ordem. Sucesso vira `sent`; erro permanente ou prazo vencido vira `failed`, sempre com o evento correspondente.
3. Recibos do WhatsApp atualizam o status só para frente e geram `status_updated`.

A v1 roda em uma única réplica: o WhatsApp derruba a sessão duplicada se duas réplicas segurarem a mesma instância.

## Desenvolvimento

```bash
gofmt -l .
go vet ./...
go test -race ./...
```

- Testes de banco usam `TEST_DATABASE_URL` e são ignorados sem ela. Rode com `-p 1`, porque compartilham o mesmo banco: `TEST_DATABASE_URL=postgres://... go test -race -p 1 ./...`.
- O teste com broker real usa `KAFKA_TEST_BROKERS` e é ignorado sem ela.
- A verificação com WhatsApp real é manual: pareamento por QR e por pair code, reconexão na subida, recebimento, mensagem enviada pelo celular e envio de cada tipo de mídia a um destinatário real. A lista completa de passos fica no histórico do projeto.

## Licença

MIT. Veja o arquivo [LICENSE](LICENSE).
