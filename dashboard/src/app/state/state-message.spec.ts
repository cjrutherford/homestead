import { describe, expect, it, beforeEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { StateMessage } from './state-message';

describe('StateMessage', () => {
  let fixture: ComponentFixture<StateMessage>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [StateMessage],
      providers: [provideRouter([])],
    }).compileComponents();
    fixture = TestBed.createComponent(StateMessage);
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('announces errors assertively', () => {
    fixture.componentRef.setInput('kind', 'error');
    fixture.componentRef.setInput('headline', 'Something failed.');
    fixture.detectChanges();
    expect(text()).toContain('Something failed.');
    expect(
      fixture.nativeElement.querySelector('[role="alert"]'),
    ).not.toBeNull();
  });

  it('announces statuses politely', () => {
    fixture.componentRef.setInput('kind', 'status');
    fixture.componentRef.setInput('headline', 'Saved.');
    fixture.detectChanges();
    expect(
      fixture.nativeElement.querySelector('[role="status"]'),
    ).not.toBeNull();
  });

  it('renders empty states without roles', () => {
    fixture.componentRef.setInput('kind', 'empty');
    fixture.componentRef.setInput('headline', 'Nothing yet.');
    fixture.detectChanges();
    expect(text()).toContain('Nothing yet.');
    expect(fixture.nativeElement.querySelector('.state-empty')).not.toBeNull();
    expect(fixture.nativeElement.querySelector('[role]')).toBeNull();
  });

  it('renders warnings as announced, warning-styled messages', () => {
    fixture.componentRef.setInput('kind', 'warning');
    fixture.componentRef.setInput('headline', 'Backups cannot be read if this appliance is lost.');
    fixture.detectChanges();
    const el = fixture.nativeElement.querySelector('.state-warning');
    expect(el?.getAttribute('role')).toBe('status');
    expect(fixture.nativeElement.querySelector('.state-status')).toBeNull();
  });

  it('announces notices politely with a neutral style', () => {
    fixture.componentRef.setInput('kind', 'notice');
    fixture.componentRef.setInput('headline', 'Nothing is running.');
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('.state-notice[role="status"]')).not.toBeNull();
  });

  it('shows a detail line and a button action that emits', () => {
    let clicks = 0;
    fixture.componentInstance.action.subscribe(() => clicks++);
    fixture.componentRef.setInput('kind', 'error');
    fixture.componentRef.setInput('headline', 'Could not load routes.');
    fixture.componentRef.setInput('detail', 'Check that homestead-server is running.');
    fixture.componentRef.setInput('actionLabel', 'Try again');
    fixture.detectChanges();
    expect(text()).toContain('Check that homestead-server is running.');
    fixture.nativeElement.querySelector('button.state-action').click();
    expect(clicks).toBe(1);
  });

  it('renders the action as a link when given a route', () => {
    fixture.componentRef.setInput('kind', 'empty');
    fixture.componentRef.setInput('headline', 'No apps installed yet.');
    fixture.componentRef.setInput('actionLabel', 'Browse the catalog');
    fixture.componentRef.setInput('actionLink', '/catalog');
    fixture.detectChanges();
    const link = fixture.nativeElement.querySelector('a.state-action');
    expect(link?.textContent).toContain('Browse the catalog');
    expect(fixture.nativeElement.querySelector('button')).toBeNull();
  });

  it('renders nothing for a null or empty headline', () => {
    fixture.componentRef.setInput('kind', 'error');
    fixture.componentRef.setInput('headline', null);
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('.state-message')).toBeNull();
    fixture.componentRef.setInput('headline', '');
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('.state-message')).toBeNull();
  });
});
