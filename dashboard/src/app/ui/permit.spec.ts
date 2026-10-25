import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { Approval, Permit, sealCode } from './permit';

@Component({
  imports: [Permit],
  template: `
    <app-permit heading="Turn on the resolver" [digest]="digest" [level]="2" [approval]="approval()" [refusal]="refusal()">
      <p class="body">Port 53 answers on your network.</p>
    </app-permit>
  `,
})
class Host {
  digest = 'sha256:0123456789abcdef';
  approval = signal<Approval | null>(null);
  refusal = signal<string | null>(null);
}

describe('Permit', () => {
  function render() {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    return { fixture, el: fixture.nativeElement as HTMLElement };
  }

  it('shows the seal with eight characters and the full digest as its name', () => {
    const { el } = render();
    const seal = el.querySelector('.permit-seal')!;
    expect(seal.getAttribute('role')).toBe('img');
    expect(seal.getAttribute('aria-label')).toBe('Plan digest sha256:0123456789abcdef');
    expect(el.querySelector('.permit-seal-code')?.textContent?.trim()).toBe('01234567');
  });

  it('names the permit by its title at the chosen level and projects the review', () => {
    const { el } = render();
    const article = el.querySelector('article.permit')!;
    const title = el.querySelector('h2.permit-title')!;
    expect(article.getAttribute('aria-labelledby')).toBe(title.id);
    expect(title.textContent).toContain('Turn on the resolver');
    expect(el.querySelector('.permit-body .body')).not.toBeNull();
    expect(article.getAttribute('data-state')).toBe('pending');
    expect(el.querySelector('.permit-stamp')).toBeNull();
  });

  it('stamps an approval with the approver and time, and a refusal with its reason', () => {
    const { fixture, el } = render();
    fixture.componentInstance.approval.set({ by: 'owner', at: '2026-10-25T14:02:00Z' });
    fixture.detectChanges();
    expect(el.querySelector('.permit')?.getAttribute('data-state')).toBe('approved');
    expect(el.querySelector('.permit-stamp')?.textContent).toContain('Approved');
    expect(el.querySelector('.permit-stamp')?.textContent).toContain('by owner, 25 Oct 2026, 14:02 UTC');

    fixture.componentInstance.approval.set(null);
    fixture.componentInstance.refusal.set('The plan changed. Review it again.');
    fixture.detectChanges();
    expect(el.querySelector('.permit')?.getAttribute('data-state')).toBe('refused');
    expect(el.querySelector('.permit-stamp')?.textContent).toContain('Not approved');
    expect(el.querySelector('.permit-reason')?.textContent).toContain('Review it again.');
  });

  it('drops an algorithm prefix from the seal code', () => {
    expect(sealCode('sha256:abcdef0123')).toBe('abcdef01');
    expect(sealCode('abcdef0123')).toBe('abcdef01');
  });
});
