/**
 * Fixed sign-in failure codes the daemon puts on the redirect back to the
 * login page (ADR-033). The codes carry no detail; the words live here.
 */
const MESSAGES: Record<string, string> = {
  unavailable: 'Authentik did not answer. Use your local password.',
  unlinked: 'That Authentik account is not linked to this appliance. Use your local password.',
  state: 'That sign-in expired or was already used. Start again, or use your local password.',
  origin:
    'Sign-in with Authentik is not set up for this address. Open Homestead at one of the addresses on the Sign-in page, or use your local password.',
  'rate-limited': 'Too many sign-in attempts. Wait a minute, or use your local password.',
  'link-conflict': 'An Authentik account is already linked. Unlink it first.',
};

/** The sentence for a failure code, or null when there is none. */
export function ssoErrorMessage(code: string | null): string | null {
  if (!code) {
    return null;
  }
  return MESSAGES[code] ?? 'Sign-in with Authentik did not finish. Use your local password.';
}

/**
 * Sentences for a failed Authentik step-up. The daemon sends the person
 * back to the dashboard root with `?sso_error=<code>` (S334); the codes are
 * the same fixed set as the login page, but the next step differs: there is
 * no local password on the public address.
 */
const STEP_UP_MESSAGES: Record<string, string> = {
  stale: "Authentik didn't ask for your password again, so the step-up wasn't accepted. Try again.",
  session: "The sign-in didn't match this dashboard session. Sign in again and retry.",
  unlinked: "That Authentik account isn't the one linked to this appliance. Sign in with the linked account and try again.",
  state: 'That step-up expired or was already used. Start it again.',
  unavailable: "Authentik didn't answer. Try again in a moment.",
  denied: 'Authentik refused the sign-in. Try again, or ask the owner.',
  'rate-limited': 'Too many sign-in attempts. Wait a minute, then try again.',
};

/** The sentence for a failed step-up code, or null when there is no code. */
export function stepUpErrorMessage(code: string | null): string | null {
  if (!code) {
    return null;
  }
  return STEP_UP_MESSAGES[code] ?? "The Authentik step-up didn't finish. Try again.";
}
