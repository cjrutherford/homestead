import { describe, expect, it, beforeEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { NotFound } from './not-found';

describe('NotFound', () => {
  let fixture: ComponentFixture<NotFound>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [NotFound],
      providers: [provideRouter([])],
    }).compileComponents();
    fixture = TestBed.createComponent(NotFound);
    fixture.detectChanges();
  });

  it('renders a way back to status', () => {
    const text = fixture.nativeElement.textContent as string;
    expect(text).toContain('Not found');
    const link = fixture.nativeElement.querySelector('a[href="/status"]');
    expect(link).not.toBeNull();
  });
});
