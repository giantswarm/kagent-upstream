---
id: codex-a-persons-chatgpt-login-as
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'feat(codex): a person''s ChatGPT login as a codex model'
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#3258, kagent-dev/kagent#3156]
  exit: with the upstreaming of the ChatGPT login and the Session provider credentials it extends
depends_on: [codex-accept-a-chatgpt-subscription-login-as, egress-the-actor-jwt-on-the-installations-providers, egress-a-sessions-provider-credentials-by]
verified: 2026-10-22
---
# feat(codex): a person's ChatGPT login as a codex model

## Change

`feat(codex): a person's ChatGPT login as a codex model`

## Reason

a ChatGPT-login ModelConfig named one labelled Secret, compiled into the agent's revision, so every Session of a codex agent ran on one person's ChatGPT subscription. A ModelConfig with `openAI.credentialType: chatgptOAuth` and no `apiKeySecret` is now the Session person's own login: the CRD admits it, the Codex compiler sets the bearer placeholder and the ChatGPT backend URL with no Secret, and no account id, so Codex sends no `chatgpt-account-id`, which the backend does not require, and the login refresher leaves it alone. The installation's Session providers (`KAGENT_SESSION_ACTOR_JWT_PROVIDERS`) take entries without an audience, which the egress gateway resolves by credential URI at the installation's credential authority under a path prefix (`chatgpt` on `chatgpt.com`, `/backend-api/codex/`, Bearer): every bound Session is admitted with them, and such an entry binds only where the revision declares that header for the Session's person (an `egress.Credential` with `fromSession`, compiled for the person's ChatGPT model and skipped by every ActorTemplate and Actor policy), so no other agent gets the person's login, whatever hosts it reaches. Both extended features are carried by this line only, so the change stays fork-only until they are upstreamed.

## History

new with this change.
