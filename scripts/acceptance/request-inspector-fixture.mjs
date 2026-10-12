import contract from './contracts/request-inspector.json' with { type: 'json' };

export const REQUEST_INSPECTOR_CONTRACT = contract;

/** The Go-certified inspector response for one contract case, as the handler sends it. */
export function requestInspectorResponse(caseId) {
  const entry = REQUEST_INSPECTOR_CONTRACT.cases.find((candidate) => candidate.id === caseId);
  if (!entry) throw new Error(`Unknown request inspector contract case: ${caseId}`);
  return { status: entry.status, headers: { 'Cache-Control': 'no-store', 'Content-Type': 'application/json; charset=utf-8' }, json: structuredClone(entry.response) };
}
