/**
 * Curated copy for the install wizard (S285): what the manifests do not say.
 * Every line below is drawn from the package manifest, docs/architecture or
 * docs/THIRD_PARTY.md, or from the upstream project's own first-run behaviour.
 * Nothing here promises a feature the package does not have. When unsure, the
 * hint stays generic and true.
 *
 * A spec fails when a package in `catalog/` has no entry, so a new package
 * cannot ship without first-login copy.
 */
export interface CuratedCopy {
  /** What the owner does first, in order. One or two short sentences each. */
  firstLogin: string[];
  /** Something to know that the manifest does not say. Optional. */
  note?: string;
  /** Replaces the sign-in paragraph where the manifest's class would mislead (Authentik). */
  signIn?: string;
  /** The port the app listens on inside its container, where the manifest declares no host port. */
  containerPort?: string;
}

const TRUST =
  'Open it from a device that trusts your Homestead root certificate (Trust page), so the browser treats the address as secure.';
const NO_ACCOUNT = 'There is no account to create.';
const NEXTCLOUD_LINK =
  'To open and save files in your Nextcloud, link it on the Craft files page.';
const NO_NEXTCLOUD = 'It has no Nextcloud file link, so files stay in your browser and download to your device.';

export const CURATED_COPY: Record<string, CuratedCopy> = {
  'actual-budget': { firstLogin: ['Open Actual Budget and choose to sign in with Authentik. Homestead already made you its server owner under your Authentik username, so nobody else can claim that role. After sign-in Actual may offer to create a budget file; that is normal use, not setup. People who sign in get ordinary accounts.'] },
  'api-client': { firstLogin: ['Open API Client and sign in or follow its setup instructions.'] },
  'bookstack': { firstLogin: ['Open BookStack and sign in or follow its setup instructions.'] },
  'circuits': { firstLogin: ['Open Circuits and sign in or follow its setup instructions.'] },
  'contacts': { firstLogin: ['Open Contacts and sign in or follow its setup instructions.'] },
  'expense': { firstLogin: ['Open Expenses and sign in or follow its setup instructions.'] },
  'inventory': { firstLogin: ['Open Inventory and sign in or follow its setup instructions.'] },
  'notes': { firstLogin: ['Open Notes and sign in or follow its setup instructions.'] },
  'parametric': { firstLogin: ['Open Parametric and sign in or follow its setup instructions.'] },
  'projects': { firstLogin: ['Open Projects and sign in or follow its setup instructions.'] },
  'records': { firstLogin: ['Open Records and sign in or follow its setup instructions.'] },
  'solitaire': { firstLogin: ['Open Solitaire and sign in or follow its setup instructions.'] },
  'sudoku': { firstLogin: ['Open Sudoku and sign in or follow its setup instructions.'] },
  'tasks': { firstLogin: ['Open Tasks and sign in or follow its setup instructions.'] },
  'three-d': { firstLogin: ['Open 3D and sign in or follow its setup instructions.'] },
  'time-tracking': { firstLogin: ['Open Time tracking and sign in or follow its setup instructions.'] },
  'vikunja': { firstLogin: ['Open Vikunja and sign in or follow its setup instructions.'] },
  'audiobookshelf': { firstLogin: ['Open Audiobookshelf and sign in or follow its setup instructions.'] },
  'calibre-web-automated': { firstLogin: ['Open Calibre-Web Automated and choose Login with Authentik. Homestead already replaced the well-known admin login (admin / admin123) with an administrator named homestead-admin; its password is under Secrets in the app card Manage panel (Reveal), for emergencies only. Your first sign-in with a name that already exists in the app signs in as that account.'] },
  'freshrss': { firstLogin: ['Open FreshRSS and choose to sign in with Authentik; Homestead already installed it, so there is no web installer. Your Authentik username is FreshRSS\'s administrator. Its own password is under Secrets in the app card Manage panel (Reveal) and works only while sign-in is off.'] },
  'gitea': { firstLogin: ['Open Gitea and choose Sign in with authentik. Homestead already created its administrator (homestead-admin); its password is under Secrets in the app card Manage panel (Reveal), for emergencies only.'] },
  'komga': { firstLogin: ['Open Komga and sign in or follow its setup instructions.'] },
  'memos': { firstLogin: ['Open Memos and choose Continue with Authentik; Homestead already created its administrator (homestead-admin), so there is no first-account screen. Its password is under Secrets in the app card Manage panel (Reveal); sign in with it at /auth/admin to administer Memos. People who sign in with Authentik are ordinary users.'] },
  'linkding': { firstLogin: ['Open Linkding and sign in or follow its setup instructions.'] },
  'mealie': { firstLogin: ['Open Mealie and choose Login with Authentik. Homestead already replaced the well-known administrator login (changeme@example.com / MyPassword) with homestead-admin@homestead.lan, so there is no first-login wizard; its password is under Secrets in the app card Manage panel (Reveal), for emergencies only. Add ?direct=1 to the login address to use it.'] },
  'navidrome': { firstLogin: ['Open Navidrome and sign in or follow its setup instructions.'] },
  'paperless-ngx': { firstLogin: ['Open Paperless-ngx and sign in or follow its setup instructions.'] },
  'uptime-kuma': { firstLogin: ['Open Uptime Kuma and sign in or follow its setup instructions.'] },
  'vaultwarden': { firstLogin: ['Open Vaultwarden and sign in or follow its setup instructions.'] },
  nextcloud: {
    containerPort: '80',
    firstLogin: [
      'Open Nextcloud. The first visit shows its setup page, where you create the administrator account.',
      'Keep that password safe. Homestead does not store it.',
      'Your Homestead address is already a trusted domain, so Nextcloud accepts it.',
    ],
    note: 'Your files live on bulk storage. To use Nextcloud from your craft apps, link it on the Craft files page.',
  },
  jellyfin: {
    firstLogin: [
      'Open Jellyfin. The first visit starts its setup wizard, where you create the administrator account.',
      'Add your media folder as a library. Jellyfin only sees folders that were added to it during install, at the path you gave inside the app.',
    ],
    note: 'Jellyfin keeps its settings on application storage and its cache on bulk storage. Your media stays where it is.',
  },
  immich: {
    firstLogin: [
      'Open Immich and register. The first account you create becomes the administrator.',
      'In the Immich phone app, enter the same address as the server. Your phone must trust the Homestead root certificate (Trust page) to use the https address.',
    ],
    note: 'Your photo library lives on bulk storage. The database password was generated for you and is kept in the secret store.',
  },
  authentik: {
    firstLogin: [
      'Open Authentik’s address itself (not a page under it): Authentik takes you to its first-run page to set the email and password for the akadmin administrator. Right after the install it can say “Request has been denied” for about a minute while it starts; reload.',
      'Then create providers and applications in Authentik for the services that should sign in through it.',
    ],
    note: 'Authentik is the sign-in service. Other apps use it; it does not use another one.',
    signIn:
      'This is the sign-in service. Other apps can sign their people in through it, with OAuth2, OpenID Connect, SAML or LDAP providers. It keeps its own accounts and does not sign in through anything else.',
  },
  wordcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open WordCraft and start a document, or open a .docx file.',
      TRUST,
      'Save your work to your device. WordCraft keeps nothing on the appliance. ' + NO_NEXTCLOUD,
    ],
  },
  gridcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open GridCraft and start a workbook, or open a workbook file.',
      TRUST,
      NEXTCLOUD_LINK,
    ],
  },
  deckcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open DeckCraft and start a presentation, or open a presentation file.',
      TRUST,
      'Save your work to your device. ' + NO_NEXTCLOUD,
    ],
  },
  pdfcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open PdfCraft and choose a PDF to read, combine, split or secure. Your documents are not uploaded.',
      TRUST,
      NEXTCLOUD_LINK,
    ],
  },
  vectorcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open VectorCraft and start drawing, or import an SVG or PDF.',
      TRUST + ' Without WebGPU it falls back to WebGL2 on its own.',
      'Download your artwork to your device. ' + NO_NEXTCLOUD,
    ],
  },
  photocraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open PhotoCraft and choose a photo to edit. Your images are not uploaded.',
      TRUST + ' Without WebGPU it falls back to WebGL2 on its own.',
      'Export your edits to your device. ' + NO_NEXTCLOUD,
    ],
  },
  lightcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open LightCraft and import photos to build your library. Your photos are not uploaded.',
      'The library lives in your browser’s site storage, which the browser may clear. Keep your original photos elsewhere and use File, Back Up Library.',
      TRUST,
    ],
    note: 'LightCraft is experimental.',
  },
  figcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open FigCraft and start a design on the canvas.',
      TRUST + ' Without WebGPU it falls back to WebGL2 on its own.',
      'Download your designs to your device. ' + NO_NEXTCLOUD,
    ],
  },
  filmcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open FilmCraft and import footage to start a project.',
      'Your footage and projects stay in your browser, with a recovery copy in browser storage. Export to your device when you finish.',
      NEXTCLOUD_LINK,
    ],
    note: 'Large media is held by the browser, so long or high-resolution projects need a device with enough memory.',
  },
  effectcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open EffectCraft and start a composition.',
      'Projects and imported media stay in your browser’s storage. Export to your device when you finish.',
      NEXTCLOUD_LINK,
    ],
    note: 'After the first visit it can start offline. Large media is held by the browser, so heavy compositions need plenty of memory.',
  },
  soundcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open SoundCraft and start a session. Your browser may need a click before it plays sound.',
      TRUST,
      'Export your sessions to your device. ' + NO_NEXTCLOUD,
    ],
    note: 'Large audio files are held by the browser, so long multitrack sessions need a device with enough memory.',
  },
  designcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open DesignCraft and start a page layout.',
      TRUST,
      'Export your documents to your device. Preferences are kept in the browser. ' + NO_NEXTCLOUD,
    ],
  },
  cadcraft: {
    firstLogin: [
      NO_ACCOUNT + ' Open CADCraft and start a drawing with its command line, or open a DXF drawing.',
      TRUST,
      NEXTCLOUD_LINK,
    ],
    note: 'Upstream marks CADCraft as early development.',
  },
};

/** The generic first step, true for any package, when a title has no curated entry. */
export const GENERIC_COPY: CuratedCopy = {
  firstLogin: ['Open the app and follow its first-run screen.'],
};

export function curatedFor(name: string): CuratedCopy {
  return CURATED_COPY[name] ?? GENERIC_COPY;
}
