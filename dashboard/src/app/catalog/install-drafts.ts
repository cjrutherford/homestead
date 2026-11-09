import { Injectable } from '@angular/core';
import { AppPublicRequest, EdgeRoute } from '../core/api.service';
import { StepId } from './wizard-model';

/** An extra folder the owner adds to an app in the Storage step. */
export interface FolderRow {
  service: string;
  class: string;
  /** `existing` mounts a folder that is already there; `new` creates one under the app's folder. */
  kind: 'existing' | 'new';
  path: string;
  target: string;
  readonly: boolean;
}

/** Everything the owner entered, kept while the dashboard stays open (S285). */
export interface InstallDraft {
  step: StepId;
  domain: string;
  hostnames: Record<string, string>;
  containerPorts: Record<string, string>;
  mountpoints: Record<string, string>;
  folders: FolderRow[];
  acknowledgedEdge: boolean;
  zone: string;
  publicIp: string;
  networks: string;
  /** The verify-and-build operation, for craft apps. */
  buildOperation: number | null;
  installOperation: number | null;
  /** The addresses the reviewed plan will give the app, added to the edge after install. */
  routes?: EdgeRoute[];
  /** S313: the owner's choice on "Also publish at" (null keeps the default) and the name. */
  publish?: boolean | null;
  publicName?: string;
  /** The public address plan approved with the install, applied by its digest once installed. */
  publicApproved?: { request: AppPublicRequest; digest: string; hostname: string } | null;
}

/**
 * In-memory drafts, one per package. Back and Next keep entries, and leaving
 * the wizard while an install runs and coming back finds the same operation.
 * Nothing is written to the browser: a reload starts clean, and the plan is
 * always asked for again, so the plan on the Review step is the one that is
 * applied.
 */
@Injectable({ providedIn: 'root' })
export class InstallDrafts {
  private readonly drafts = new Map<string, InstallDraft>();

  get(name: string): InstallDraft | undefined {
    return this.drafts.get(name);
  }

  save(name: string, draft: InstallDraft): void {
    this.drafts.set(name, draft);
  }

  clear(name: string): void {
    this.drafts.delete(name);
  }
}
