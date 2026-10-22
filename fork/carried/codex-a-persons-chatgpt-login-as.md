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
  refs: [kagent-dev/kagent#3257, kagent-dev/kagent#3263, kagent-dev/kagent#3258, agent-substrate/substrate#2541]
  exit: "the ModelConfig passthrough target is bound by the platform's egress policy compiler (kagent-dev/kagent#3263) to the muster credential URI, which carries no person (muster resolves the person from the actor's binding): the compiler part drops at the re-pin onto acc75416b; the path part follows giantswarm/substrate:egress-scopes-credential-header-to-request-path-prefix; re-check each re-pin"
depends_on: [codex-accept-a-chatgpt-subscription-login-as, egress-the-actor-jwt-on-the-installations-providers, giantswarm/substrate:egress-scopes-credential-header-to-request-path-prefix]
verified: 2026-10-22
---
# feat(codex): a person's ChatGPT login as a codex model

## Change

`feat(codex): a person's ChatGPT login as a codex model`

## Reason

a ChatGPT-login ModelConfig named one labelled Secret, compiled into the agent's revision, so every Session of a codex agent ran on one person's ChatGPT subscription. A ModelConfig with `openAI.credentialType: chatgptOAuth` and no `apiKeySecret` is now the Session person's own login: the CRD admits it, the Codex compiler sets the bearer placeholder and the ChatGPT backend URL with no Secret, and no account id, so Codex sends no `chatgpt-account-id`, which the backend does not require, and the login refresher leaves it alone. The installation's Session providers (`KAGENT_SESSION_ACTOR_JWT_PROVIDERS`) take entries without an audience, which the egress gateway resolves by credential URI at the installation's credential authority under a path prefix (`chatgpt` on `chatgpt.com`, `/backend-api/codex/`, Bearer): every bound Session is admitted with them, and such an entry binds only where the revision declares that header for the Session's person (an `egress.Credential` with `fromSession`, compiled for the person's ChatGPT model and skipped by every ActorTemplate and Actor policy), so no other agent gets the person's login, whatever hosts it reaches. Both extended features are carried by this line only, so the change stays fork-only until they are upstreamed.

## History

new with this change.

2026-10-22, decided on internal issue 38652, comment 6340015449: class 3 platform for the binding, class 6 for the path scope; the path prefix `/backend-api/codex/` keeps a person's account-wide ChatGPT token off the rest of chatgpt.com, so this row is a second consumer of the path_prefix family and its 2026-11-17 fallback is a re-decision, not a drop (the ChatGPT case is to be added to agent-substrate/substrate#2541); destination the platform compiler (theme T9's binary); first step the compiler; test the Codex translator's ChatGPT cases and the lab's ChatGPT proof; depends on themes T6 and T9; re-check 2026-11-17; owner Quentin.
