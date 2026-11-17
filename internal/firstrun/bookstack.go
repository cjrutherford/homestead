package firstrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"homestead/internal/catalog"
)

// BookStackSeededAccount is the administrator every BookStack database is
// born with (email admin@admin.com, password "password"). The routine
// takes that password away the first time the app answers, whether or not
// sign-in with Authentik is ever turned on (S366).
const BookStackSeededAccount = "admin@admin.com"

// bookstackScript runs inside the BookStack container with its own php.
// The new password arrives on stdin as JSON, never argv or environment;
// the database password is read from the file Homestead mounted
// (FILE__DB_PASSWORD) and never printed. It prints one JSON line:
// {"state": "waiting"|"created"|"ready"|"failed", "detail": ...}.
//
// It changes the account only while "password" still opens it, in one
// compare-and-set UPDATE, so an administrator the owner already changed
// (or deleted) is left exactly as it is. It waits until every migration
// file has been applied, so the seeded account cannot appear afterwards.
//
// VERIFIED AGAINST BookStack v26.09.2: users(email, password,
// remember_token, updated_at), the migrations table and
// /app/www/database/migrations.
const bookstackScript = `
$out = function ($state, $detail = '') { echo json_encode(['state' => $state, 'detail' => $detail]), "\n"; exit(0); };
$in = json_decode(stream_get_contents(STDIN), true);
if (!is_array($in) || empty($in['account']) || empty($in['password'])) $out('failed', 'the account and password are missing');
$file = getenv('FILE__DB_PASSWORD');
$pw = $file ? rtrim((string) @file_get_contents($file), "\r\n") : '';
if ($pw === '' || !getenv('DB_HOST') || !getenv('DB_DATABASE') || !getenv('DB_USERNAME')) $out('waiting', 'the database settings are not readable yet');
try {
  $pdo = new PDO('mysql:host=' . getenv('DB_HOST') . ';port=' . (getenv('DB_PORT') ?: '3306') . ';dbname=' . getenv('DB_DATABASE') . ';charset=utf8mb4', getenv('DB_USERNAME'), $pw, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION, PDO::ATTR_TIMEOUT => 5]);
  $files = count(glob('/app/www/database/migrations/*.php'));
  $done = (int) $pdo->query('SELECT COUNT(*) FROM migrations')->fetchColumn();
} catch (Throwable $e) { $out('waiting', 'the database is not ready'); }
if ($files < 1 || $done < $files) $out('waiting', 'BookStack is still migrating its database');
try {
  $cols = $pdo->query("SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'users'")->fetchAll(PDO::FETCH_COLUMN);
  foreach (['id', 'email', 'password', 'remember_token', 'updated_at'] as $c) if (!in_array($c, $cols, true)) $out('failed', 'table users lacks column ' . $c . ' (re-verify this routine against the BookStack schema)');
  $q = $pdo->prepare('SELECT id, password FROM users WHERE email = ?');
  $q->execute([$in['account']]);
  $row = $q->fetch(PDO::FETCH_ASSOC);
  if (!$row) $out('ready', 'no seeded administrator');
  if (!password_verify('password', $row['password'])) $out('ready', 'the account already has its own password');
  $hash = password_hash($in['password'], PASSWORD_BCRYPT);
  $u = $pdo->prepare('UPDATE users SET password = ?, remember_token = NULL, updated_at = NOW() WHERE id = ? AND password = ?');
  $u->execute([$hash, $row['id'], $row['password']]);
  $q->execute([$in['account']]);
  $now = $q->fetch(PDO::FETCH_ASSOC);
  if (!$now || !password_verify($in['password'], $now['password']) || password_verify('password', $now['password'])) $out('failed', 'the seeded password was not replaced');
  $out('created');
} catch (Throwable $e) { $out('failed', 'the database access failed'); }
`

func bookstackStep(ctx context.Context, container, account, password string) (Result, bool, error) {
	input, _ := json.Marshal(map[string]string{"account": account, "password": password})
	raw, err := execIn(ctx, container, string(input), "php", "-r", bookstackScript)
	raw = strings.ReplaceAll(raw, password, "[redacted]")
	if err != nil {
		return "", false, errors.New(strings.ReplaceAll(err.Error(), password, "[redacted]"))
	}
	var reply struct{ State, Detail string }
	if json.Unmarshal([]byte(lastLine(raw)), &reply) != nil {
		return "", false, errors.New("BookStack returned an unexpected first-run response")
	}
	switch reply.State {
	case "created":
		return Created, true, nil
	case "ready":
		return Ready, true, nil
	case "waiting":
		return "", false, errors.New("BookStack is starting: " + reply.Detail)
	}
	return "", true, errors.New("BookStack first run failed: " + reply.Detail)
}

// bookstackFirstRun replaces the seeded administrator's well-known
// password with a generated one the owner can reveal (the break-glass
// login when sign-in is off), and leaves a changed or removed account
// alone. Until it finishes, BookStack's seeded administrator
// admin@admin.com still opens with the password "password".
func bookstackFirstRun(ctx context.Context, m catalog.Manifest, secrets Secrets) (Result, error) {
	f := m.FirstRun
	if f == nil || m.Name != "bookstack" || f.Account != BookStackSeededAccount {
		return "", errors.New("BookStack first-run configuration is unsupported")
	}
	password, err := Password(m, secrets)
	if err != nil {
		return "", err
	}
	container := m.Name + "-" + f.Service
	res, err := waitFor(ctx, m.Title, func() (Result, bool, error) {
		return bookstackStep(ctx, container, f.Account, password)
	})
	if err != nil {
		return "", errors.New(err.Error() + " (until this finishes BookStack's seeded administrator " + BookStackSeededAccount + " still opens with the password \"password\": change it in BookStack under My Account)")
	}
	return res, nil
}
