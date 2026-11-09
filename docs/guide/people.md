# People and sign-in

Authentik is the one place accounts live. Homestead manages the people you give access to, and the apps they may open, from the **People** page or `homesteadctl people`.

People management needs Authentik sign-in linked with a **kept API token** (the Sign-in page, "Keep the token"). Without one, the page tells you so and links to the opt-in. Homestead creates, changes and removes only people it created or that you adopted, and only its own `homestead-` groups. Nothing else in Authentik is touched.

## Adding a person

On **People**, choose **Add a person**: name, username, optional email, the apps they can open and how they first sign in. The options are a one-time link, a generated password or a password you choose. A link or generated password is shown once and is not kept.

```
homesteadctl people add --help
homesteadctl people list
homesteadctl people reset-password alexreed --generate
```

One-time links rely on an Authentik recovery flow. Homestead creates its own (`homestead-recovery`) when the sign-in plan runs and the default brand has none. If that could not be set up, the person is still kept and the dialog offers **Generate a password instead**.

## Grants and closed by default

Apps are **closed by default**. A person can open an app only when you grant it, and you can take it away again.

```
homesteadctl people grant alexreed jellyfin
homesteadctl people grant alexreed gitea --admin
homesteadctl people revoke alexreed jellyfin
homesteadctl people apps alexreed
```

An app admin is an administrator inside that one app. Nobody but you is ever an administrator of Homestead. The install wizard can also grant people an app as part of the install.

## Other actions

- **Reset password** and **Reset second factor** (`people reset-password`, `people reset-mfa`).
- **Disable** and **Enable** a person without deleting them.
- **Remove** deletes their Authentik account. Their data stays inside the apps.
- **Adopt** brings an Authentik account Homestead did not create under its management (`people adopt USERNAME`). It is the only way Homestead touches such an account.

Each of these asks you to confirm first.

## When Authentik and Homestead disagree

If someone changes a person in Authentik directly, People shows **Differs in Authentik** and changes nothing until you choose.

- **Put back** restores what Homestead recorded. This is the recommended choice.
- **Keep** accepts what Authentik has now.
- If the account was deleted in Authentik, choose **Recreate** to make it again with a fresh sign-in link or generated password, or **Forget** to remove the person from Homestead.

The command line equivalent is `homesteadctl people resolve PERSON put-back|keep|recreate|forget`.

## Recovery

- **A person forgot their password.** The public Authentik sign-in page has no "Forgot password" link. They ask you, and you choose **Reset password** to send a new one-time link or generated password.
- **You are locked out of Authentik sign-in.** Sign in to the dashboard with your local password on your home network. `homesteadctl sso disable` turns Authentik sign-in off.
- **You lost the owner password.** On the appliance, run `sudo homesteadctl admin recover`.

## Step-up on the public name

On your home network, risky actions (password reset, second-factor reset, making an app admin, removing a person, settling a difference) ask for your local password again. The local password is never accepted on a public name. There, the dashboard sends you through a fresh Authentik sign-in instead, and for five minutes that session may run these People actions. Each step-up and action is audited. Everything else that needs a fresh check, such as changing sign-in settings, revealing a secret or exporting the recovery key, works only on your home network.
