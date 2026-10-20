import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideRouter } from '@angular/router';
import { Subject } from 'rxjs';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { ApiService, OperationState } from '../core/api.service';
import { Edge } from './edge';

const DOC = JSON.stringify({
  routes: [
    {
      Host: 'app.lan',
      Target: { Host: 'app', Port: 8080 },
      Exposure: 'local',
      TLS: 'internal',
      Health: { Path: '/', IntervalSeconds: 30 },
      Policy: { Name: 'lan', Sources: [{ Zone: 'lan', CIDR: '' }] },
    },
  ],
});

describe('Edge', () => {
  let fixture: ComponentFixture<Edge>;
  let http: HttpTestingController;
  let events: Subject<OperationState>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Edge],
      providers: [provideHttpClient(), provideHttpClientTesting(), provideRouter([])],
    }).compileComponents();
    events = new Subject();
    vi.spyOn(TestBed.inject(ApiService), 'operationEvents').mockReturnValue(events);
    fixture = TestBed.createComponent(Edge);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
    http.expectOne('/api/v1/edge/routes').flush({ routes: [] });
    fixture.detectChanges();
  });

  /** Terminal operations re-read the recorded table. */
  const settleReload = (routes: unknown[] = []) => {
    http.expectOne('/api/v1/edge/routes').flush({ routes });
    fixture.detectChanges();
  };
  afterEach(() => http.verify());

  const c = () => fixture.componentInstance as any;
  const text = () => fixture.nativeElement.textContent as string;
  const click = (label: string) => {
    const button = Array.from(
      fixture.nativeElement.querySelectorAll('button') as NodeListOf<HTMLButtonElement>,
    ).find((b) => b.textContent?.trim() === label);
    expect(button, label).toBeTruthy();
    button!.click();
    fixture.detectChanges();
  };
  const state = (status: string, steps: string[] = []): OperationState =>
    ({
      id: 7,
      type: 'edge',
      status,
      error: status === 'failed' ? 'caddy exploded' : '',
      steps: steps.map((name, id) => ({ id, name, status: 'done' })),
    }) as unknown as OperationState;

  it('shows the empty table state and links to exposure', () => {
    expect(text()).toContain('No routes in the table');
    expect(fixture.nativeElement.querySelector('a[href="/exposure"]')).toBeTruthy();
  });

  it('confirms before deploying and then streams progress', () => {
    click('Deploy Caddy');
    expect(text()).toContain('briefly interrupts every route');
    http.expectNone('/api/v1/edge/deploy');
    click('Confirm deploy');
    const req = http.expectOne('/api/v1/edge/deploy');
    expect(req.request.method).toBe('POST');
    req.flush({ id: 7 });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/operations/7"]')).not.toBeNull();
    expect(text()).toContain('Starting');
    events.next(state('running', ['pull']));
    fixture.detectChanges();
    expect(text()).toContain('Pull');
    events.next(state('succeeded', ['pull', 'up']));
    settleReload();
    expect(text()).not.toContain('Starting');
  });

  it('cancelling the deploy confirmation sends nothing', () => {
    click('Deploy Caddy');
    click('Cancel');
    expect(fixture.nativeElement.querySelector('[role=alertdialog]')).toBeNull();
  });

  it('shows a failed operation error', () => {
    click('Deploy Caddy');
    click('Confirm deploy');
    http.expectOne('/api/v1/edge/deploy').flush({ id: 7 });
    events.next(state('failed'));
    settleReload();
    expect(text()).toContain('caddy exploded');
  });

  it('maps a network failure to readable text', () => {
    click('Deploy Caddy');
    click('Confirm deploy');
    http.expectOne('/api/v1/edge/deploy').error(new ProgressEvent('error'));
    fixture.detectChanges();
    expect(text()).toContain("Can't reach the appliance");
  });

  it('reports a lost progress stream', () => {
    click('Deploy Caddy');
    click('Confirm deploy');
    http.expectOne('/api/v1/edge/deploy').flush({ id: 7 });
    events.error(new Error('x'));
    fixture.detectChanges();
    expect(text()).toContain('Lost the live progress stream');
  });

  it('validates routes without calling the API', () => {
    click('Add route');
    c().setField(0, 'host', 'App.lan');
    c().setField(0, 'targetHost', 'http://app');
    c().setField(0, 'port', '70000');
    c().setField(0, 'healthPath', 'x');
    c().setField(0, 'interval', '3');
    fixture.detectChanges();
    click('Reconcile routes');
    for (const part of ['lowercase', 'not a URL', 'port must be', 'start with /', '0 or 5-300']) {
      expect(text()).toContain(part);
    }
    expect(fixture.nativeElement.querySelector('[role=alertdialog]')).toBeNull();
  });

  it('rejects duplicate hosts', () => {
    c().importJson(DOC);
    click('Add route');
    c().setField(1, 'host', 'app.lan');
    c().setField(1, 'targetHost', 'b');
    c().setField(1, 'port', '80');
    fixture.detectChanges();
    click('Reconcile routes');
    expect(text()).toContain('app.lan appears more than once');
  });

  it('reconciles a loaded document after confirmation, keeping policy', () => {
    c().importJson(DOC);
    fixture.detectChanges();
    expect(text()).toContain('Loaded 1 route');
    click('Reconcile routes');
    expect(text()).toContain('replaces the route table');
    click('Confirm reconcile');
    const req = http.expectOne('/api/v1/edge/routes');
    expect(req.request.body.routes[0]).toEqual({
      Host: 'app.lan',
      Target: { Host: 'app', Port: 8080 },
      Exposure: 'local',
      TLS: 'internal',
      Health: { Path: '/', IntervalSeconds: 30 },
      Policy: { Name: 'lan', Sources: [{ Zone: 'lan', CIDR: '' }] },
    });
    req.flush({ id: 9 });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/operations/9"]')).not.toBeNull();
  });

  it('warns about public routes and about emptying the table', () => {
    click('Reconcile routes');
    expect(text()).toContain('empties the table');
    click('Cancel');
    click('Add route');
    c().setField(0, 'host', 'pub.example.com');
    c().setField(0, 'targetHost', 'app');
    c().setField(0, 'port', '80');
    c().setField(0, 'exposure', 'public');
    fixture.detectChanges();
    click('Reconcile routes');
    expect(text()).toContain('pub.example.com will be reachable from the public');
    http.expectNone('/api/v1/edge/routes');
  });

  it('surfaces a daemon validation refusal', () => {
    c().importJson(DOC);
    click('Reconcile routes');
    click('Confirm reconcile');
    http
      .expectOne('/api/v1/edge/routes')
      .flush('route.host: bad', { status: 422, statusText: 'Unprocessable' });
    fixture.detectChanges();
    expect(text()).toContain('route.host: bad');
  });

  it('rejects malformed route documents', () => {
    c().importJson('{nope');
    fixture.detectChanges();
    expect(text()).toContain('not valid JSON');
    c().importJson('{}');
    fixture.detectChanges();
    expect(text()).toContain('needs a "routes" list');
  });

  it('removes a route row', () => {
    c().importJson(DOC);
    fixture.detectChanges();
    click('Remove route');
    expect(text()).toContain('No routes in the table');
  });

  it('shows the recorded table on open and keeps policy', () => {
    const again = TestBed.createComponent(Edge);
    again.detectChanges();
    http.expectOne('/api/v1/edge/routes').flush({ routes: JSON.parse(DOC).routes });
    again.detectChanges();
    const input = again.nativeElement.querySelector('input[name=host-0]') as HTMLInputElement;
    expect(input.value).toBe('app.lan');
    expect(again.nativeElement.textContent).toContain('exposure policy');
  });

  it('disables reconcile until the table loads and shows a load failure', () => {
    const again = TestBed.createComponent(Edge);
    again.detectChanges();
    const reconcile = () =>
      (Array.from(again.nativeElement.querySelectorAll('button') as NodeListOf<HTMLButtonElement>)
        .find((b) => b.textContent?.trim() === 'Reconcile routes'))!;
    expect(again.nativeElement.querySelector('app-loading[aria-busy="true"]')).not.toBeNull();
    expect(reconcile().disabled).toBe(true);
    http.expectOne('/api/v1/edge/routes').flush('boom', { status: 500, statusText: 'x' });
    again.detectChanges();
    expect(again.nativeElement.textContent).toContain('Could not load the route table: boom');
    expect(reconcile().disabled).toBe(true);
    // Retry succeeds.
    (Array.from(again.nativeElement.querySelectorAll('button') as NodeListOf<HTMLButtonElement>)
      .find((b) => b.textContent?.trim() === 'Try loading routes again'))!.click();
    http.expectOne('/api/v1/edge/routes').flush({ routes: [] });
    again.detectChanges();
    expect(reconcile().disabled).toBe(false);
  });

  it('mirrors the private-name automatic TLS refusal', () => {
    click('Add route');
    c().setField(0, 'host', 'app.lan');
    c().setField(0, 'targetHost', 'app');
    c().setField(0, 'port', '80');
    c().setField(0, 'tls', 'auto');
    fixture.detectChanges();
    click('Reconcile routes');
    expect(text()).toContain('automatic TLS needs a public name');
    c().setField(0, 'host', 'wiki.test');
    fixture.detectChanges();
    click('Reconcile routes');
    expect(text()).toContain('automatic TLS needs a public name');
    c().setField(0, 'host', 'wiki.mydomain.org');
    fixture.detectChanges();
    click('Reconcile routes');
    expect(text()).not.toContain('automatic TLS needs a public name');
  });

  it('locks the form while an operation runs and unsubscribes the previous stream', () => {
    click('Deploy Caddy');
    click('Confirm deploy');
    http.expectOne('/api/v1/edge/deploy').flush({ id: 7 });
    events.next(state('running'));
    fixture.detectChanges();
    const buttons = Array.from(
      fixture.nativeElement.querySelectorAll('button') as NodeListOf<HTMLButtonElement>,
    );
    expect(buttons.find((b) => b.textContent?.trim() === 'Deploy Caddy')!.disabled).toBe(true);
    expect(buttons.find((b) => b.textContent?.trim() === 'Reconcile routes')!.disabled).toBe(true);
    expect(events.observed).toBe(true);
    // A new operation replaces the stream rather than stacking one.
    const second = new Subject<OperationState>();
    (TestBed.inject(ApiService).operationEvents as any).mockReturnValue(second);
    events.next(state('succeeded'));
    settleReload();
    click('Deploy Caddy');
    click('Confirm deploy');
    http.expectOne('/api/v1/edge/deploy').flush({ id: 8 });
    expect(events.observed).toBe(false);
    expect(second.observed).toBe(true);
  });

  it('reports each duplicate host once', () => {
    for (let i = 0; i < 3; i++) {
      click('Add route');
      c().setField(i, 'host', 'dup.lan');
      c().setField(i, 'targetHost', 'a');
      c().setField(i, 'port', '80');
    }
    fixture.detectChanges();
    click('Reconcile routes');
    expect((text().match(/dup\.lan appears more than once/g) ?? []).length).toBe(1);
  });

  it('caps the pasted document size and validates item schema', () => {
    c().importJson('{"routes":[],"pad":"' + 'x'.repeat(256 * 1024) + '"}');
    fixture.detectChanges();
    expect(text()).toContain('larger than 256 KiB');
    c().importJson('{"routes":[42]}');
    fixture.detectChanges();
    expect(text()).toContain('Route 1 must be an object');
    c().importJson('{"routes":[{"Host":"a.lan","Target":{"Host":"a","Port":"80"},"Exposure":"local","TLS":"off"}]}');
    fixture.detectChanges();
    expect(text()).toContain('Target needs a string Host and a numeric Port');
    c().importJson('{"routes":[{"Host":"a.lan","Target":{"Host":"a","Port":80},"Exposure":"world","TLS":"off"}]}');
    fixture.detectChanges();
    expect(text()).toContain('Exposure must be local, overlay or public');
  });

  it('leaves the dashboard route to the Trust page (S289)', () => {
    c().loadRoutes();
    http.expectOne('/api/v1/edge/routes').flush({
      routes: [
        { Host: 'homestead.lan', Dashboard: true, Aliases: ['homestead.local'], Exposure: 'local', TLS: 'internal' },
        { Host: 'app.lan', Target: { Host: 'app', Port: 8080 }, Exposure: 'local', TLS: 'internal', Health: { Path: '', IntervalSeconds: 0 } },
      ],
    });
    fixture.detectChanges();
    expect(c().drafts().map((d: { host: string }) => d.host)).toEqual(['app.lan']);
  });
});
