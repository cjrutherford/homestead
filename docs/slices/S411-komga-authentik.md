# S411: Komga signs in through Authentik

## Goal
Turn the `env-oidc` adapter on for Komga 1.29.1 (Spring Security OAuth2
client), keep the local recovery login working, and let a private
(appliance-root) issuer be trusted by the JVM without losing its public CAs.

## Facts
- Komga config via environment (Spring relaxed binding, dots to underscores,
  dashes removed): https://komga.org/docs/installation/oauth2 and
  https://komga.org/docs/installation/configuration.
- Redirect URI is `{baseUrl}/{action}/oauth2/code/{registrationId}`, so
  `/login/oauth2/code/authentik`; sign-in starts at
  `/oauth2/authorization/authentik` (`SecurityConfiguration.kt`, 1.29.1).
  `server.forward-headers-strategy: framework` is already Komga's default
  (`application.yml`), so `{baseUrl}` follows the proxy's forwarded host.
- Komga matches an OAuth2 user to an existing Komga user by email.
  `KOMGA_OAUTH2ACCOUNTCREATION=true` creates an ordinary (non-admin) user
  when none matches. `email_verified` is checked by default; Authentik sends
  it true. There is no group or role mapping, so nobody is promoted.
- Komga's login page is its own web app. Spring `formLogin` is disabled in
  both branches of the security config, and `/api/v1/oauth2/providers` only
  adds provider buttons beside the password form, so the local recovery
  account keeps working (confirmed from source; a live check is still due).
- Provider config: either `issuer-uri` or all four explicit URIs. Spring
  discovers `issuer-uri` at startup, which would stop Komga booting while
  Authentik is down, so explicit URIs are used and the OIDC `iss` check
  (which needs the issuer) is not.
- The image (`Dockerfile.tpl`, 1.29.1) is Ubuntu with a copied Temurin JRE
  and `ENTRYPOINT ["java", "-Dspring.profiles.include=docker",
  "--enable-native-access=ALL-UNNAMED", "-jar", "application.jar",
  "--spring.config.additional-location=file:/config/"]` in `/app`. It does
  not use the Temurin image's entrypoint, so `USE_SYSTEM_CA_CERTS` does not
  exist here.

## Change
- `catalog/komga.yaml`: `sso.oidc` env-oidc; `entrypoint` set to the image's
  java command so the existing supervisor can exec it; secret
  `oidc_client_secret` consumed as
  `SPRING_SECURITY_OAUTH2_CLIENT_REGISTRATION_AUTHENTIK_CLIENTSECRET_FILE`
  with `export: true` (Spring has no `_FILE`; the supervisor is the existing
  machinery, a config tree import would be new code and a file per property).
- Environment set: registration `PROVIDER`, `CLIENTID`, `CLIENTNAME`
  (button label), `SCOPE=openid,profile,email`,
  `AUTHORIZATIONGRANTTYPE=authorization_code`; provider `AUTHORIZATIONURI`,
  `TOKENURI`, `USERINFOURI` (from `{{authentik_base}}/application/o/...`),
  `JWKSETURI` (`{{issuer}}jwks/`); `KOMGA_OAUTH2ACCOUNTCREATION=true`
  (Authentik access is closed by default, ADR-043). Client secret is never a
  set value.
- Trust: `EnvOIDC.trustbundle: java`. For the appliance's own issuer,
  `JDK_JAVA_OPTIONS` (read by the `java` launcher, so Komga's
  `JAVA_TOOL_OPTIONS` heap cap is untouched) names
  `-Djavax.net.ssl.trustStore=/etc/homestead/ca/bundle.jks` with password
  `changeit`. `bundle.jks` is a JKS written with the standard library
  (no PKCS12 or third-party dependency) holding the host's public CAs plus
  the appliance root, in the existing read-only CA directory, mode 0644.
  A public issuer (Let's Encrypt) sets nothing; turning sign-in off or a
  public issuer removes the variable (value-checked) and both bundle files.
- Existing installs: Komga stacks predate any supervisor, so
  `WireDeclaredSecret` now also puts an exporting service under the
  supervisor (entrypoint, command = image start plus existing command,
  script mount) when its compose lacks it; supervised stacks are untouched.

## Tests
JKS read by `keytool` (when installed), error cases, trust per issuer,
the full plan/apply/public/off lifecycle on a pre-supervisor install
(secret in no file, idempotent rewiring), manifest pins and validation.

## Out of scope
Recorded in DEFERRED.
