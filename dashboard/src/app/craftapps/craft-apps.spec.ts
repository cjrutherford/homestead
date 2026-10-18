import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { provideRouter } from '@angular/router';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { CraftApp } from '../core/api.service';
import { CraftApps } from './craft-apps';

const app = (over: Partial<CraftApp> = {}): CraftApp => ({
  name: 'gridcraft',
  title: 'GridCraft',
  version: '0.7.0',
  status: 'installed',
  url: 'https://gridcraft.lan/',
  ...over,
});

describe('CraftApps', () => {
  let fixture: ComponentFixture<CraftApps>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [CraftApps],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(CraftApps);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });
  afterEach(() => http.verify());

  const text = () => fixture.nativeElement.textContent as string;
  const load = (apps: CraftApp[]) => {
    http.expectOne('/api/v1/craft/apps').flush({ apps });
    fixture.detectChanges();
  };

  it('shows a loading state first', () => {
    expect(fixture.nativeElement.querySelector('app-loading[aria-busy="true"]')).not.toBeNull();
    load([]);
  });

  it('shows the empty state through StateMessage', () => {
    load([]);
    expect(text()).toContain('No craft apps are installed');
    expect(fixture.nativeElement.querySelectorAll('[data-testid="craft-app"]').length).toBe(0);
  });

  it('links each app in a new tab with rel=noopener', () => {
    load([app(), app({ name: 'pdfcraft', title: 'PdfCraft', url: 'https://pdfcraft.lan/' })]);
    const links = Array.from(fixture.nativeElement.querySelectorAll('a')) as HTMLAnchorElement[];
    expect(links.map((a) => a.getAttribute('href'))).toEqual([
      'https://gridcraft.lan/',
      'https://pdfcraft.lan/',
    ]);
    for (const a of links) {
      expect(a.getAttribute('target')).toBe('_blank');
      expect(a.getAttribute('rel')).toContain('noopener');
    }
    expect(text()).toContain('version 0.7.0');
  });

  it('lists an app without a route with no link and the reason', () => {
    load([app({ url: '', url_reason: 'No edge route serves this app yet.' })]);
    expect(fixture.nativeElement.querySelectorAll('a').length).toBe(0);
    expect(fixture.nativeElement.querySelector('[data-testid="no-url"]').textContent).toContain(
      'No edge route serves this app yet.',
    );
  });

  it('shows an error with retry when the load fails', () => {
    http.expectOne('/api/v1/craft/apps').flush('boom', { status: 500, statusText: 'x' });
    fixture.detectChanges();
    expect(text()).toContain('boom');
    (fixture.nativeElement.querySelector('button') as HTMLButtonElement).click();
    load([app()]);
    expect(text()).toContain('GridCraft');
  });
});
