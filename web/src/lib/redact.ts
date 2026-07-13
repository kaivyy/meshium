// Client-side redaction fallback (4G slice 3F).
//
// The backend already centralizes secret redaction, but the directive's rule #5
// is "when in doubt, redact more." Any backend-provided string rendered into
// the UI — event messages, audit data, toasts, exports — is passed through
// redactForDisplay() so a backend regression cannot leak a secret into the
// operator's screen. Patterns mirror the server's redaction (PGPASSWORD via env,
// connection-string creds, CLI auth flags). Never increases length of truthful
// content; only masks credential-shaped substrings.

const PATTERNS: { re: RegExp; mask: string }[] = [
  // Connection-string credentials: postgres://user:pass@host or mysql://...
  { re: /(:\/\/[^:/@]+:)([^@]+)(@)/g, mask: '$1••••••••$3' },
  // Generic -pPASSWORD style flags (mysql/mysqldump attach the password with no
  // space). Excludes a purely-numeric attached value so real CLI port args
  // like -p6379 are not masked.
  { re: /(-p)(?![0-9]+\b)\S+/g, mask: '$1••••••' },
  // REDISCLI_AUTH / PGPASSWORD env assignments
  { re: /(REDISCLI_AUTH|PGPASSWORD)\s*=\s*\S+/gi, mask: '$1=••••••' },
  // Bearer tokens
  { re: /(Bearer\s+)[A-Za-z0-9\-._~+/]+=*/gi, mask: '$1••••••' },
  // Authorization: header values
  { re: /(Authorization:\s*)(\S+)/gi, mask: '$1••••••' },
  // password JSON keys
  { re: /("password"\s*:\s*")([^"]*)(")/gi, mask: '$1••••••$3' },
  { re: /("secret"\s*:\s*")([^"]*)(")/gi, mask: '$1••••••$3' },
  // token query params
  { re: /([?&](?:token|access_token|api_key|apikey)=)[^&\s]+/gi, mask: '$1••••••' },
];

export function redactForDisplay(input: string | null | undefined): string {
  if (!input) return '';
  let out = input;
  for (const { re, mask } of PATTERNS) {
    out = out.replace(re, mask);
  }
  return out;
}
