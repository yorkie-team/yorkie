---
title: external-identity-login
target-version: 0.7.29
---

# External Identity Login

## Problem

Dashboard login supports username/password and GitHub OAuth only. Teams that
host Yorkie themselves ask to log in with their organization's SSO instead.
Those systems often do not follow OAuth2 or OIDC: no client secret, tokens
passed as parameters, calls allowed only from fixed IPs. Accepting each
variation as server code or config would pile organization-specific behavior
into the open-source server.

### Goals

- An operator connects a login method with config and one separate service,
  without changing the yorkie or dashboard images.
- No server code, config key, or dashboard string names an organization's
  login service.
- Users are identified by `(auth_provider, subject)`, so the same name under
  another provider never signs in to someone else's account.
- GitHub and username/password login keep working as they do.
- The dashboard learns the enabled login methods at runtime.

### Non-Goals

- Shipping any organization-specific broker.
- Built-in OIDC. It can be added later as another `Provider`.
- External login for the CLI.
- Moving existing accounts between providers (see Migration).

## Design

The organization's login protocol lives in a **broker** the operator runs.
Yorkie trusts only a short-lived assertion the broker signs.

```
dashboard -> GET  /auth/external/{name}/login
             yorkie issues a state, sets the state cookie, redirects to
          -> broker LoginURL?state=...
             the broker signs the user in (outside Yorkie)
broker    -> POST /auth/external/{name}/callback   form field `assertion`
             yorkie verifies it, finds or creates (external:{name}, sub),
             sets the session cookie, 303-redirects to the dashboard
```

The assertion travels through the browser, as an auto-submitted form, so
Yorkie and the broker need no network path between them. A broker can be a
small service behind the organization's web-server auth module that reads the
user from request headers and signs.

### Providers

`server/rpc/auth` has a `Provider` interface (`Name`, `LoginURL`, `Callback`).
GitHub and each external provider implement it. `AuthManager` issues and
consumes the state, finds or creates the user, and sets the cookie.

- A state is bound to the route that issued it, to the browser through the
  `__Host-yorkie-auth-state` cookie (`SameSite=None` so a broker on another
  site can post the callback; the prefix keeps sibling subdomains from
  planting it), and expires after 10 minutes. At most 10,000 logins wait at
  once. Starting a second login in another tab replaces the first one's
  cookie, so the first fails with `invalid state`.
- The callback answers 303 so the browser follows with a GET; a 307 would
  post the assertion on to the dashboard.
- The callback takes `application/x-www-form-urlencoded` up to 64 KiB.
- External users get `auth_provider: "external:{name}"`, so no configured
  name can collide with `github` or local users.
- `GET /auth/providers` returns
  `{"passwordLogin": true, "providers": [{"name", "displayName", "loginPath"}]}`.
  GitHub is listed only when `GitHubClientID` is set.

### Users

`users` has `subject` next to `username`, unique per `auth_provider` (a
partial index on `subject` existing). Local and GitHub users use their
username as subject; every start backfills it for users stored before.

A provider login finds the user by `(auth_provider, subject)`. A new user gets
`preferred_username`, or `sub` without it. If another user holds that
username, or it is not a valid Yorkie username (2–30 of `a-z 0-9 - . _ ~`),
the login is refused. A returning user keeps the username it was created
with.

### Config

```yaml
RPC:
  Auth:
    # The browser goes here after any provider login.
    GitHubCallbackRedirectURL: "https://dashboard.example.com/dashboard"
    ExternalProviders:
      - Name: corp
        DisplayName: "Company SSO"
        LoginURL: "https://login-broker.example.com/login"
        Issuer: "login-broker"
        Audience: "yorkie-admin"
        PublicKeys:
          - |
            -----BEGIN PUBLIC KEY-----
            ...
            -----END PUBLIC KEY-----
```

Without a config file, put the list in a YAML file and pass
`--auth-external-providers-file`. The helm chart takes
`yorkie.args.externalProviders` and does that through a ConfigMap. The server
refuses to start on an invalid name, URL, or key, or a duplicate name.
`LoginURL` must be https, or http on localhost, because it carries the
state.

Generate a key pair for the broker:

```sh
openssl ecparam -name prime256v1 -genkey -noout -out broker.key  # broker only
openssl ec -in broker.key -pubout -out broker.pub                 # PublicKeys
```

During a key rotation, list the old and the new public key.

### Assertion

A JWT signed with ES256. Any other `alg` is refused.

| Claim | Required | Meaning |
|-------|----------|---------|
| `iss` | yes | equals `Issuer` |
| `aud` | yes | contains `Audience` |
| `sub` | yes | the user's ID in the organization; never changes for a person and is never reused for another |
| `state` | yes | the `state` query parameter the broker received, unchanged |
| `iat`, `exp` | yes | `exp - iat` at most 60 seconds; 5 seconds of clock skew allowed |
| `jti` | yes | unique per assertion; a reused one is refused |
| `preferred_username` | no | username of a new user |

A broker must sign users in itself before signing, put the received `state`
back unchanged, and post the assertion with a form rather than a query
string, which access logs and browser history would keep. A leaked state
lets someone else finish that login in the victim's browser, so the broker
should send `Referrer-Policy: no-referrer` and keep `state` out of its logs.

```go
// The broker, after it has signed the user in.
claims := jwt.MapClaims{
	"iss": "login-broker", "aud": "yorkie-admin",
	"sub": employeeID, "preferred_username": loginName,
	"state": r.URL.Query().Get("state"),
	"iat": now.Unix(), "exp": now.Add(30 * time.Second).Unix(),
	"jti": uuid.NewString(),
}
assertion, _ := jwt.NewWithClaims(jwt.SigningMethodES256, claims).SignedString(privateKey)

fmt.Fprintf(w, `<form method="post" action="%s"><input type="hidden" name="assertion" value="%s"></form>
<script>document.forms[0].submit()</script>`,
	html.EscapeString("https://yorkie.example.com/auth/external/corp/callback"),
	html.EscapeString(assertion))
```

### Migration

Project ownership and membership refer to the user `_id`, so an existing
account moves to an external provider by changing two fields:

```js
db.users.updateOne(
  { username: "alice", auth_provider: "github" },
  { $set: { auth_provider: "external:corp", subject: "employee-1001" } },
)
```

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A stolen assertion is replayed | 60-second lifetime, single-use `jti`, single-use state bound to the browser, POST only |
| Another site starts or forwards a login (login CSRF) | The state cookie must match the state in the assertion |
| The server config leaks | It holds public keys only, which cannot sign |
| A broker rotates its key | `PublicKeys` takes several keys |
| Several pods split the state and `jti` stores | `/auth` must stick to one pod, as GitHub login already requires; a shared store is a follow-up |
| A user created by an older server during a rolling update has no subject | It cannot use a provider login until a new server restarts and backfills it |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Keep the login protocol in a broker | Each organization's SSO differs from the standards differently; Yorkie keeps one generic feature |
| Asymmetric signatures, ES256 only | A leaked server config must not mint logins; one algorithm leaves no algorithm confusion, and RS256 can be added without breaking configs |
| Refuse a taken username instead of suffixing | Project invites go by username; `alice-2` invites the wrong person |
| The state is Yorkie's | A careless broker cannot weaken login CSRF protection |
| `external:` prefix on the auth provider | A provider named `github` cannot take over GitHub users |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| A configurable generic OAuth2 provider | Options for "no secret", "token in the query", extra parameters grow per organization |
| Providers compiled in by the operator | Operators would build and maintain their own server binary |
| A reverse proxy that authenticates and passes a user header | SDK traffic reaches the same server; guarding the header against forgery falls on every operator |
| OIDC only | SSO systems that do not speak OIDC still could not connect |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
