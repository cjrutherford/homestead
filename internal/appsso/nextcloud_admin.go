package appsso

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// S500: Nextcloud administrators follow the claim's "admin" alias.
//
// user_oidc hashes every group id it provisions (sha256 of
// "<provider>_0_<name>") while unique ids are on, and unique ids cannot be
// turned off without renaming every account that already signed in. So the
// alias group arrives as a hashed group whose display name is "admin", never
// as Nextcloud's real `admin` group, and the owner was not an administrator.
// user_oidc has no setting that maps a claim value to a real group.
//
// Homestead therefore installs a small first-party Nextcloud app,
// homestead_sso, whose login listener runs right after user_oidc has
// provisioned the person's groups and before the first page loads. For a
// person whose account user_oidc owns it puts them in the real `admin`
// group when they hold the alias group and takes them out when they do not,
// so removing a person from the app's admins group removes their
// administrator rights at their next sign-in. Accounts of other backends
// (the local install admin) are never touched.

const (
	nextcloudAdminApp = "homestead_sso"
	nextcloudAppDir   = "/var/www/html/custom_apps/" + nextcloudAdminApp
)

// nextcloudAdminFiles are the files of the homestead_sso app, by path under
// nextcloudAppDir.
var nextcloudAdminFiles = []struct{ Path, Body string }{
	{"appinfo/info.xml", `<?xml version="1.0"?>
<info xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="https://apps.nextcloud.com/schema/apps/info.xsd">
	<id>homestead_sso</id>
	<name>Homestead sign-in</name>
	<summary>Makes members of the Homestead admins group Nextcloud administrators</summary>
	<description>Written by HAI Homestead. After a sign-in through user_oidc, a person in the claim's admin group is put in Nextcloud's admin group, and one who is not is taken out of it.</description>
	<version>1.0.0</version>
	<licence>agpl</licence>
	<author>HAI Homestead</author>
	<namespace>HomesteadSso</namespace>
	<category>integration</category>
	<dependencies>
		<nextcloud min-version="28" max-version="99"/>
	</dependencies>
</info>
`},
	{"lib/AppInfo/Application.php", `<?php

declare(strict_types=1);

namespace OCA\HomesteadSso\AppInfo;

use OCA\HomesteadSso\Listener\LoginListener;
use OCP\AppFramework\App;
use OCP\AppFramework\Bootstrap\IBootContext;
use OCP\AppFramework\Bootstrap\IBootstrap;
use OCP\AppFramework\Bootstrap\IRegistrationContext;
use OCP\User\Events\UserLoggedInEvent;

class Application extends App implements IBootstrap {
	public const APP_ID = 'homestead_sso';

	public function __construct() {
		parent::__construct(self::APP_ID);
	}

	public function register(IRegistrationContext $context): void {
		$context->registerEventListener(UserLoggedInEvent::class, LoginListener::class);
	}

	public function boot(IBootContext $context): void {
	}
}
`},
	{"lib/Listener/LoginListener.php", `<?php

declare(strict_types=1);

namespace OCA\HomesteadSso\Listener;

use OCP\EventDispatcher\Event;
use OCP\EventDispatcher\IEventListener;
use OCP\IGroupManager;
use OCP\User\Events\UserLoggedInEvent;
use Psr\Container\ContainerInterface;
use Psr\Log\LoggerInterface;

/** @template-implements IEventListener<UserLoggedInEvent> */
class LoginListener implements IEventListener {
	public function __construct(
		private IGroupManager $groups,
		private LoggerInterface $logger,
		private ContainerInterface $container,
	) {
	}

	/**
	 * The ids user_oidc gives the Homestead provider's admins group: the
	 * hash of the provider's database id and of its identifier (both ways a
	 * release may key it). Both are scoped to the Homestead provider.
	 *
	 * @return string[]
	 */
	private function adminGroupIds(): array {
		$keys = ['authentik'];
		try {
			$mapper = $this->container->get('OCA\\UserOIDC\\Db\\ProviderMapper');
			$keys[] = (string)$mapper->findProviderByIdentifier('authentik')->getId();
		} catch (\Throwable $e) {
			$this->logger->warning('homestead_sso: the Homestead provider was not found: ' . $e->getMessage());
		}
		$ids = [];
		foreach ($keys as $key) {
			$ids[] = hash('sha256', $key . '_0_admin');
		}
		return $ids;
	}

	public function handle(Event $event): void {
		if (!($event instanceof UserLoggedInEvent)) {
			return;
		}
		$user = $event->getUser();
		if ($user->getBackendClassName() !== 'user_oidc') {
			return;
		}
		// user_oidc hashes group ids: sha256 of "<provider>_0_<name>". Admin
		// is granted only for the exact id of the Homestead provider's
		// admins group, never for another group whose display name is
		// "admin" (S507).
		$want = false;
		foreach ($this->adminGroupIds() as $gid) {
			$group = $this->groups->get($gid);
			if ($group !== null && $group->getGID() !== 'admin' && $group->inGroup($user)) {
				$want = true;
				break;
			}
		}
		$admin = $this->groups->get('admin') ?? $this->groups->createGroup('admin');
		if ($admin === null) {
			return;
		}
		$has = $admin->inGroup($user);
		if ($want && !$has) {
			$admin->addUser($user);
			$this->logger->info('homestead_sso: ' . $user->getUID() . ' is now an administrator');
		} elseif (!$want && $has) {
			$admin->removeUser($user);
			$this->logger->info('homestead_sso: ' . $user->getUID() . ' is no longer an administrator');
		}
	}
}
`},
}

// installAdminApp writes the homestead_sso app into the container and
// enables it.
func (a nextcloud) installAdminApp(ctx context.Context, t Target) error {
	const write = `mkdir -p "$(dirname "$1")" && cat > "$1.tmp" && mv "$1.tmp" "$1"`
	for _, f := range nextcloudAdminFiles {
		if _, err := execIn(ctx, t.Container, "www-data", f.Body, "sh", "-c", write, "sh", nextcloudAppDir+"/"+f.Path); err != nil {
			return fmt.Errorf("appsso: write the %s app: %w", nextcloudAdminApp, err)
		}
	}
	if _, err := a.occ(ctx, t, "", "app:enable", nextcloudAdminApp); err != nil {
		return fmt.Errorf("appsso: enable the %s app: %w", nextcloudAdminApp, err)
	}
	return nil
}

// RemoveNextcloudAdmin takes a person out of Nextcloud's real admin group
// now (S507), so a revoked, disabled or removed person does not keep
// administrator rights until their next sign-in. The person is found among
// the accounts user_oidc owns by email (or, when unique ids are off, by
// uid); the local install admin and any other backend are never touched.
// It returns nil when the person has no Nextcloud account or is not an
// administrator. Callers must not let a failure block their own action.
func RemoveNextcloudAdmin(ctx context.Context, container, username, email string) error {
	nc := nextcloud{}
	t := Target{Container: container}
	out, err := nc.occ(ctx, t, "", "user:list", "--info", "--output=json", "--limit=-1")
	if err != nil {
		return fmt.Errorf("appsso: list Nextcloud accounts: %w", err)
	}
	var users map[string]struct {
		UserID  string   `json:"user_id"`
		Email   string   `json:"email"`
		Backend string   `json:"backend"`
		Groups  []string `json:"groups"`
	}
	if err := json.Unmarshal([]byte(out), &users); err != nil {
		return fmt.Errorf("appsso: read the Nextcloud account list: %w", err)
	}
	for uid, u := range users {
		if u.Backend != "user_oidc" || uid == "admin" {
			continue
		}
		byMail := email != "" && strings.EqualFold(u.Email, email)
		byUID := username != "" && strings.EqualFold(uid, username)
		if !byMail && !byUID {
			continue
		}
		admin := false
		for _, g := range u.Groups {
			if g == "admin" {
				admin = true
			}
		}
		if !admin {
			continue
		}
		if _, err := nc.occ(ctx, t, "", "group:removeuser", "admin", uid); err != nil {
			return fmt.Errorf("appsso: take %s out of Nextcloud's admin group: %w", uid, err)
		}
	}
	return nil
}
