package appsso

import (
	"context"
	"fmt"
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
use Psr\Log\LoggerInterface;

/** @template-implements IEventListener<UserLoggedInEvent> */
class LoginListener implements IEventListener {
	public function __construct(
		private IGroupManager $groups,
		private LoggerInterface $logger,
	) {
	}

	public function handle(Event $event): void {
		if (!($event instanceof UserLoggedInEvent)) {
			return;
		}
		$user = $event->getUser();
		if ($user->getBackendClassName() !== 'user_oidc') {
			return;
		}
		// user_oidc hashes group ids; the claim's admin alias is the hashed
		// group whose display name is "admin".
		$want = false;
		foreach ($this->groups->search('admin') as $group) {
			if ($group->getGID() !== 'admin' && $group->getDisplayName() === 'admin' && $group->inGroup($user)) {
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
