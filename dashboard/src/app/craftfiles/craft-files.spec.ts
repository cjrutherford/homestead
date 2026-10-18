import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { CraftFiles } from './craft-files';

const UNLINKED = {
  linked: false,
  credential_stored: false,
  daemon_created: false,
  apps: [
    { name: 'gridcraft', title: 'GridCraft', mode: 'open-save', local_only: false, host: 'gridcraft.lan', ready: false },
    { name: 'filmcraft', title: 'FilmCraft', mode: 'open-only', local_only: false, ready: false },
  ],
};

const LINKED = {
  linked: true,
  base_url: 'https://cloud.lan',
  account: 'alice',
  root: 'Homestead',
  credential_stored: true,
  daemon_created: false,
  apps: [
    { name: 'gridcraft', title: 'GridCraft', mode: 'open-save', local_only: false, host: 'gridcraft.lan', ready: true },
    { name: 'filmcraft', title: 'FilmCraft', mode: 'open-only', local_only: true, ready: false },
  ],
};

describe('CraftFiles', () => {
  let fixture: ComponentFixture<CraftFiles>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [CraftFiles],
      providers: [provideHttpClient(), provideHttpClientTesting(), provideRouter([])],
    }).compileComponents();
    fixture = TestBed.createComponent(CraftFiles);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => http.verify());

  function load(status: object): HTMLElement {
    http.expectOne('/api/v1/craft/files').flush(status);
    fixture.detectChanges();
    return fixture.nativeElement as HTMLElement;
  }

  it('shows an unlinked appliance with browser-local apps and no launch', () => {
    const el = load(UNLINKED);
    expect(el.textContent).toContain('Not linked');
    expect(el.textContent).toContain('browser-local (not linked)');
    const launch = el.querySelector('tbody button') as HTMLButtonElement;
    expect(launch.disabled).toBe(true);
  });

  it('plans a link, then approves it with the password sent once and cleared', () => {
    const el = load(UNLINKED);
    const set = (name: string, value: string) => {
      const input = el.querySelector(`input[name="${name}"]`) as HTMLInputElement;
      input.value = value;
      input.dispatchEvent(new Event('input'));
    };
    set('base-url', 'https://cloud.lan');
    set('account', 'alice');
    set('app-password', 'secret-app-password');
    (el.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit'));
    const planReq = http.expectOne('/api/v1/craft/files/plan');
    expect(planReq.request.body).toEqual({
      action: 'link',
      base_url: 'https://cloud.lan',
      account: 'alice',
      root: 'Homestead',
      create_app_password: false,
    });
    expect(JSON.stringify(planReq.request.body)).not.toContain('secret-app-password');
    planReq.flush({ id: 'p1', action: 'link', summary: ['Link it.'], warnings: ['Homestead enters the file path.'] });
    fixture.detectChanges();
    expect(el.textContent).toContain('Homestead enters the file path.');
    const approve = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes('Approve')) as HTMLButtonElement;
    approve.click();
    const approveReq = http.expectOne('/api/v1/craft/files/approve');
    expect(approveReq.request.body).toEqual({
      plan_id: 'p1',
      app_password: 'secret-app-password',
      account_password: undefined,
    });
    approveReq.flush(LINKED);
    fixture.detectChanges();
    expect(el.textContent).not.toContain('secret-app-password');
    expect(el.textContent).toContain('Linked to https://cloud.lan as alice');
    expect((fixture.componentInstance as unknown as { appPassword: string }).appPassword).toBe('');
  });

  it('launches an app in a new window and toggles the browser-local opt-out', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    const el = load(LINKED);
    expect(el.textContent).toContain('browser-local (opted out)');
    (el.querySelector('tbody button') as HTMLButtonElement).click();
    const launchReq = http.expectOne('/api/v1/craft/files/launch');
    expect(launchReq.request.body).toEqual({ app: 'gridcraft' });
    launchReq.flush({ url: 'https://gridcraft.lan/_homestead/open.html#code=abc', app: 'gridcraft', expires_at: '' });
    expect(open).toHaveBeenCalledWith('https://gridcraft.lan/_homestead/open.html#code=abc', '_blank', 'noopener');
    const box = el.querySelector('tbody input[type="checkbox"]') as HTMLInputElement;
    box.checked = true;
    box.dispatchEvent(new Event('change'));
    const toggle = http.expectOne('/api/v1/craft/files/local-only');
    expect(toggle.request.body).toEqual({ app: 'gridcraft', local_only: true });
    toggle.flush(LINKED);
    open.mockRestore();
  });

  it('shows daemon errors', () => {
    load(UNLINKED);
    fixture.componentInstance.review('link');
    http.expectOne('/api/v1/craft/files/plan').flush('invalid request: base_url must be an http(s) URL', { status: 422, statusText: 'x' });
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).textContent).toContain('base_url must be an http(s) URL');
  });
});
