import type { RequestInspector } from '../../web/src/types/usageEvents';

export interface RequestInspectorWireCase {
  id: string;
  request_id: string;
  relay: { status: number; fixture?: string; body?: string };
  status: number;
  response: RequestInspector;
}
export const REQUEST_INSPECTOR_CONTRACT: { version: number; description: string; cases: RequestInspectorWireCase[] };
export function requestInspectorResponse(caseId: string): {
  status: number;
  headers: Record<string, string>;
  json: RequestInspector;
};
