import { describe, expect, it, beforeEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { StateMessage } from './state-message';

describe('StateMessage', () => {
  let fixture: ComponentFixture<StateMessage>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [StateMessage],
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

  it('renders empty and loading states without roles', () => {
    fixture.componentRef.setInput('kind', 'loading');
    fixture.componentRef.setInput('headline', 'Loading…');
    fixture.detectChanges();
    expect(text()).toContain('Loading…');
    expect(fixture.nativeElement.querySelector('[role]')).toBeNull();
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

  it('renders nothing for a null or empty headline', () => {
    fixture.componentRef.setInput('kind', 'error');
    fixture.componentRef.setInput('headline', null);
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('p')).toBeNull();
    fixture.componentRef.setInput('headline', '');
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('p')).toBeNull();
  });
});
