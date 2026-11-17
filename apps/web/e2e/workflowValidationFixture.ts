import type { Route } from '@playwright/test';
import { validateNodes } from '../../../packages/agents/src/validation.mjs';

// SDK imports stay in Playwright's Node process. The app still uses HTTP only.
// Exercise the real pure rules without reading sources or starting model work.
export const validationIdentity = { runtimeFingerprint: 'a'.repeat(64), runtimeFingerprintVersion: 1 };
export async function fulfillWorkflowValidation(route: Route) {
  const definition = route.request().postDataJSON();
  try {
    validateNodes(definition.instruction, definition.nodes);
    return route.fulfill({ json: { ...validationIdentity, valid: true } });
  } catch (error) {
    const issue = error as Error & {nodeId?: string};
    return route.fulfill({ json: { ...validationIdentity, valid: false, issue: { nodeId: issue.nodeId, message: issue.message } } });
  }
}
