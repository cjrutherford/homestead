import { describe, expect, it, beforeEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { SchemaForm } from './schema-form';
import { ConfigField, FieldValues } from './schema';

const FIELDS: ConfigField[] = [
  { name: 'hostname', type: 'string', description: 'Name.', required: true },
  { name: 'web_port', type: 'integer', description: 'Port.', min: 1, max: 65535 },
  { name: 'debug', type: 'boolean', description: 'Verbose.' },
  { name: 'theme', type: 'choice', description: 'Theme.', choices: ['light', 'dark'] },
  { name: 'api_token', type: 'string', description: 'Token.', sensitive: true },
  { name: 'trace', type: 'boolean', description: 'Tracing.', advanced: true },
];

describe('SchemaForm', () => {
  let fixture: ComponentFixture<SchemaForm>;

  function setup(values: FieldValues): void {
    fixture.componentRef.setInput('fields', FIELDS);
    fixture.componentRef.setInput('values', values);
    fixture.detectChanges();
  }

  function element(): HTMLElement {
    return fixture.nativeElement as HTMLElement;
  }

  function submit(): FieldValues | undefined {
    let emitted: FieldValues | undefined;
    fixture.componentInstance.submitted.subscribe((values) => {
      emitted = values;
    });
    element()
      .querySelector('form')
      ?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    fixture.detectChanges();
    return emitted;
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [SchemaForm],
    }).compileComponents();
    fixture = TestBed.createComponent(SchemaForm);
  });

  it('renders one control per type', () => {
    setup({ hostname: 'hs', web_port: 9090, debug: true, theme: 'dark' });
    const el = element();
    expect(el.querySelector('input[name="hostname"]')?.getAttribute('type')).toBe('text');
    const port = el.querySelector('input[name="web_port"]');
    expect(port?.getAttribute('type')).toBe('number');
    expect(port?.getAttribute('min')).toBe('1');
    expect(port?.getAttribute('max')).toBe('65535');
    expect(el.querySelector('input[name="debug"]')?.getAttribute('type')).toBe('checkbox');
    expect(el.querySelector('select[name="theme"] option')?.textContent?.trim()).toBe('light');
  });

  it('masks sensitive fields', () => {
    setup({});
    expect(
      element().querySelector('input[name="api_token"]')?.getAttribute('type'),
    ).toBe('password');
  });

  it('hides advanced fields until toggled', () => {
    setup({});
    expect(element().querySelector('input[name="trace"]')).toBeNull();
    const toggle = element().querySelector(
      'form > label > input[type="checkbox"]',
    ) as HTMLElement;
    toggle.click();
    fixture.detectChanges();
    expect(element().querySelector('input[name="trace"]')).not.toBeNull();
  });

  it('emits coerced values on submit', () => {
    setup({ hostname: 'hs', web_port: 9090, debug: true, theme: 'dark' });
    expect(submit()).toEqual({
      hostname: 'hs',
      web_port: 9090,
      debug: true,
      theme: 'dark',
    });
  });

  it('omits empty optionals but always reports booleans and selections', () => {
    setup({});
    // Text/number inputs stay empty and are omitted; the select always has
    // a selection and checkboxes always report.
    expect(submit()).toEqual({ debug: false, theme: 'light' });
  });

  it('keeps current values of collapsed advanced fields', () => {
    setup({ trace: true });
    expect(submit()).toEqual({ debug: false, theme: 'light', trace: true });
  });

  it('shows per-field errors', () => {
    setup({ hostname: 'hs' });
    fixture.componentRef.setInput('errors', [
      { field: 'web_port', reason: 'want <= 65535' },
    ]);
    fixture.detectChanges();
    const alert = element().querySelector('[role="alert"]');
    expect(alert?.textContent).toContain('want <=');
  });
});
