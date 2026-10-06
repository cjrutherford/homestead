import { inject, Injectable } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiService, WizardState } from '../core/api.service';

/**
 * Resumable wizard progress (S036): thin wrapper saving each wizard's step
 * plus non-sensitive collected input. Payloads hold UI progress only —
 * tokens, passwords, and sessions never belong here (S019/S020).
 */
@Injectable({ providedIn: 'root' })
export class WizardService {
  private readonly api = inject(ApiService);

  load(id: string): Observable<WizardState> {
    return this.api.wizardState(id);
  }

  save(id: string, step: string, payload: Record<string, unknown> = {}): Observable<WizardState> {
    return this.api.saveWizardState(id, step, payload);
  }

  clear(id: string): Observable<void> {
    return this.api.clearWizardState(id);
  }
}
