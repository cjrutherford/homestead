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
  });
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
    expect(text()).toContain('Operation 7');
    expect(text()).toContain('Working');
    events.next(state('running', ['pull']));
    fixture.detectChanges();
    expect(text()).toContain('pull: done');
    events.next(state('succeeded', ['pull', 'up']));
    fixture.detectChanges();
    expect(text()).toContain('Operation succeeded.');
    expect(text()).not.toContain('Working');
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
    fixture.detectChanges();
    expect(text()).toContain('caddy exploded');
  });

  it('maps a network failure to readable text', () => {
    click('Deploy Caddy');
    click('Confirm deploy');
    http.expectOne('/api/v1/edge/deploy').error(new ProgressEvent('error'));
    fixture.detectChanges();
    expect(text()).toContain('Cannot reach the Homestead daemon');
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
    expect(text()).toContain('Operation 9');
  });

  it('warns about public routes and about emptying the table', () => {
    click('Reconcile routes');
    expect(text()).toContain('empties the route table');
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
});
