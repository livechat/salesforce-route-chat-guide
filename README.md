# Text Chat Routing from Salesforce

Looks up a customer's Salesforce Contact by email when a Text chat starts, saves their plan tier on their Text profile, and routes Standard and Enterprise chats to the appropriate team.

See the [full setup guide](https://www.text.com/docs/guides/salesforce-chat-routing).

## Quickstart

Requires a Text personal access token with the `customers:rw` and `chats--all:rw` scopes. If the service needs to create either target team, the token must belong to a Text administrator and include `groups--all:rw`. If both teams already exist, `groups--all:ro` is sufficient. The service also requires a Salesforce External Client App with the Client Credentials Flow, running as an integration user that can read Contacts.

Follow the setup guide above to prepare the Salesforce tier field, create the Salesforce app, expose the service at a public HTTPS address, and register the Text webhook.

```shell
git clone https://github.com/livechat/salesforce-route-chat-user.git
cd salesforce-route-chat-user
cp .env.example .env
```

Fill in the required values in `.env`. If the Regular or Enterprise team doesn't exist, also provide its agent IDs. Keep `.env` private because it contains Text and Salesforce credentials.

```shell
docker compose up -d --build
docker compose logs --follow salesforce-router
```

When the logs show `Salesforce chat routing ready`, stop following the logs with <kbd>Ctrl</kbd>+<kbd>C</kbd>, then check the health endpoint:

```shell
curl --fail --include http://localhost:8080/healthz
```

A healthy service returns HTTP `204`.

## Configuration

The Text app calls routing destinations teams. The APIs and this service call the same resources groups.

| Variable                     | Required                   | Default                                      | Description                                                                                                                                                            |
| ---------------------------- | -------------------------- | -------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `TEXT_BASIC_AUTH`            | Yes                        | —                                            | Base64-encoded `account_id:personal_access_token` credential from Text.                                                                                                |
| `TEXT_WEBHOOK_SECRET`        | Yes                        | —                                            | Secret Text generates for the `incoming_chat` webhook.                                                                                                                 |
| `TEXT_CONFIGURATION_URL`     | No                         | `https://api.livechatinc.com/v3.6/configuration` | Configuration API base URL.                                                                                                                                            |
| `TEXT_AGENT_URL`             | No                         | `https://api.livechatinc.com/v3.6/agent`     | Agent Chat API base URL.                                                                                                                                               |
| `TEXT_CDP_URL`               | No                         | `https://api.text.com/cdp`                   | Customer Data Platform API base URL.                                                                                                                                   |
| `TEXT_REGULAR_GROUP_NAME`    | No                         | `Regular`                                    | Name of the group that receives Standard-tier chats. Created if missing.                                                                                               |
| `TEXT_ENTERPRISE_GROUP_NAME` | No                         | `Enterprise`                                 | Name of the group that receives Enterprise-tier chats. Created if missing.                                                                                             |
| `TEXT_REGULAR_AGENT_IDS`     | If the group doesn't exist | —                                            | Comma-separated Text agent logins for the Regular team. For a group that exists, the service verifies any logins you provide but doesn't add them.                     |
| `TEXT_ENTERPRISE_AGENT_IDS`  | If the group doesn't exist | —                                            | Comma-separated Text agent logins for the Enterprise team. For a group that exists, the service verifies any logins you provide but doesn't add them.                  |
| `SALESFORCE_LOGIN_URL`       | Yes                        | —                                            | Your organization's My Domain URL, without a path.                                                                                                                      |
| `SALESFORCE_CLIENT_ID`       | Yes                        | —                                            | Consumer Key of the Salesforce External Client App.                                                                                                                    |
| `SALESFORCE_CLIENT_SECRET`   | Yes                        | —                                            | Consumer Secret of the Salesforce External Client App.                                                                                                                 |
| `SALESFORCE_PLAN_FIELD`      | Yes                        | `Plan_Tier__c` in `.env.example`             | API Name of the Contact field that holds the plan tier.                                                                                                                |
| `SALESFORCE_API_VERSION`     | No                         | `61.0`                                       | Salesforce REST API version.                                                                                                                                           |
| `PORT`                       | No                         | `8080`                                       | Port the service listens on.                                                                                                                                           |

For an existing group, the service checks any agent IDs supplied in `.env`. Without supplied IDs, it only checks for a member whose priority is not `supervisor`; it doesn't distinguish a human agent from the AI Agent. Confirm human-agent membership in the Text app.

## Stopping, tests, rebuilding

```shell
docker compose down    # stop the service
```

Stopping the service doesn't delete the `salesforce_plan_tier` property, the groups, or the webhook in Text.

Run the tests with `go test ./...` (Go 1.27). After changing the code, for example to rename or add a tier in `internal/router/handler.go`, rebuild and restart:

```shell
docker compose up -d --build
```

## License

Licensed under the [MIT License](LICENSE).

---

See the full [Text documentation reference](https://www.text.com/docs).
