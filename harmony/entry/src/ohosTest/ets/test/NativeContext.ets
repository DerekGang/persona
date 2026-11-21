import { application, common } from '@kit.AbilityKit';
import { abilityDelegatorRegistry } from '@kit.TestKit';

export const TEST_MODULE: string = 'entry_test';

// Test rawfiles are packaged in the feature HAP. The application resource manager can resolve the entry HAP instead.
export async function nativeTestModuleContext(): Promise<common.Context> {
  const app = abilityDelegatorRegistry.getAbilityDelegator().getAppContext();
  return application.createModuleContext(app, TEST_MODULE);
}

let context: common.UIAbilityContext | undefined;
export function bindNativeTestContext(value: common.UIAbilityContext): void { context = value; }
export function nativeTestContext(): common.UIAbilityContext {
  if (!context) throw new Error('test_context_unavailable');
  return context;
}
