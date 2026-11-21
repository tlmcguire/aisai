import type { AppController } from '../app';
import type { ProviderInfo } from '../api/types';
import { h } from '../lib/dom';
import { useAuditStore } from '../state/audit';
import { useUiStore } from '../state/ui';

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
  const packageInput = h('input', {
    type: 'text',
    id: 'package-input',
    placeholder: 'flask',
    value: 'flask',
    'aria-label': 'Package name',
    size: 18,
  });

  const versionInput = h('input', {
    type: 'text',
    id: 'version-input',
    placeholder: 'latest',
    value: '2.0.0',
    'aria-label': 'Package version',
    size: 10,
  });

  const scanButton = button('Scan', 'primary');
  const llmButton = button('Scan LLM');

  const providerChip = h('span', { class: 'badge neutral', text: 'checking provider…' });
  const mockBadge = h('span', { class: 'badge mock', text: 'MOCK DATA', hidden: true });
  const settingsButton = h('button', { class: 'ghost', type: 'button', title: 'Settings' }, '⚙');

  scanButton.addEventListener('click', () => {
    const pkg = packageInput.value.trim();
    if (!pkg) {
      packageInput.focus();
      return;
    }
    void controller.startAudit(pkg, versionInput.value.trim());
  });

  llmButton.addEventListener('click', () => {
    void controller.runScan('llm');
  });

  settingsButton.addEventListener('click', () => {
    useUiStore.getState().setSettingsOpen(true);
  });

  container.append(
    h('div', { class: 'brand', text: 'AISAI' }),
    h(
      'div',
      { class: 'field' },
      h('label', { for: 'package-input', text: 'Package' }),
      packageInput,
    ),
    h(
      'div',
      { class: 'field' },
      h('label', { for: 'version-input', text: 'Version' }),
      versionInput,
    ),
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
  });

  return {
    element: container,
    refreshProviders,
    dispose: () => {
      unsubscribeAudit();
      unsubscribeBackend();
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
