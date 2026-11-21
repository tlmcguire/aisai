import type { AppController } from '../app';
import type { ProviderInfo } from '../api/types';
import { h } from '../lib/dom';
import { useAuditStore } from '../state/audit';
import { useUiStore } from '../state/ui';
import { createPackagePicker } from './packagePicker';

/** Toggle a button into its busy state (spinner + disabled). */
export function setBusy(button: HTMLButtonElement, busy: boolean, label?: string): void {
  button.disabled = busy;
  button.classList.toggle('btn-busy', busy);
  const existing = button.querySelector('.spinner');
  if (busy && !existing) {
    button.prepend(h('span', { class: 'spinner', 'aria-hidden': 'true' }));
  } else if (!busy && existing) {
    existing.remove();
  }
  if (label) {
    const text = button.querySelector('.btn-label');
    if (text) text.textContent = label;
  }
}

function button(label: string, className = ''): HTMLButtonElement {
  return h('button', { class: className, type: 'button' }, h('span', { class: 'btn-label', text: label }));
}

export interface Toolbar {
  element: HTMLElement;
  refreshProviders: () => void;
  dispose: () => void;
}

export function createToolbar(controller: AppController, container: HTMLElement): Toolbar {
  const picker = createPackagePicker(controller);

  const scanButton = button('Scan', 'primary');
  const llmButton = button('Scan LLM');

  const providerChip = h('span', { class: 'badge neutral', text: 'checking provider…' });
  const mockBadge = h('span', { class: 'badge mock', text: 'MOCK DATA', hidden: true });
  const settingsButton = h(
    'button',
    { class: 'ghost icon', type: 'button', title: 'Settings', 'aria-label': 'Settings' },
    '⚙',
  );

  picker.onScan((packageName, version) => {
    void controller.startAudit(packageName, version);
  });

  scanButton.addEventListener('click', () => {
    const pkg = picker.packageName();
    if (!pkg) {
      picker.focus();
      return;
    }
    void controller.startAudit(pkg, picker.version());
  });

  llmButton.addEventListener('click', () => {
    void controller.runScan('llm');
  });

  settingsButton.addEventListener('click', () => {
    useUiStore.getState().setSettingsOpen(true);
  });

  container.append(
    h('div', { class: 'brand', text: 'AISAI' }),
    picker.element,
    scanButton,
    llmButton,
    h('div', { class: 'toolbar-spacer' }),
    mockBadge,
    providerChip,
    settingsButton,
  );

  const refreshProviders = async (): Promise<void> => {
    const providers = await controller.fetchProviders();
    providerChip.replaceChildren(...providerChips(providers));
  };

  const unsubscribeAudit = useAuditStore.subscribe((state) => {
    setBusy(scanButton, state.busy === 'audit');
    setBusy(llmButton, state.busy?.startsWith('scan:') ?? false);
  });

  const unsubscribeBackend = window.aisai.onBackendStatus((status) => {
    mockBadge.hidden = status.mode !== 'mock';
    // The first refresh happens while the sidecar is still starting, so
    // it sees no providers at all. When the backend becomes ready the
    // list is fetched again, otherwise the chip would keep saying that
    // no provider is configured for the rest of the session.
    if (status.state === 'ready') {
      void refreshProviders();
    }
  });

  return {
    element: container,
    refreshProviders,
    dispose: () => {
      unsubscribeAudit();
      unsubscribeBackend();
      picker.dispose();
    },
  };
}

function providerChips(providers: ProviderInfo[]): Node[] {
  if (!providers.length) {
    return [h('span', { class: 'badge neutral', text: 'no providers configured' })];
  }
  return providers.map((provider) =>
    h(
      'span',
      {
        class: `badge ${provider.ok ? 'ok' : 'bad'}`,
        title: provider.error ?? `${provider.id} (${provider.kind})`,
      },
      provider.ok ? '●' : '○',
      ` ${provider.id} ${provider.ok ? 'up' : 'down'}`,
    ),
  );
}
