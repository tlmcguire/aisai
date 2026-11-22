/**
 * The toolbar, which is also the window's title bar: the package picker,
 * Pull, Open folder, the two scans, the chip naming the project being
 * inspected, and at the right edge (always in view) the update notice, the
 * model server status, the terminal and Settings. It wraps onto a second row
 * in a narrow window instead of hiding anything. Dropping a folder on the
 * window audits it.
 */

import type { AppController } from '../app';
import type { UpdateStatus } from '../api/bridge';
import type { ProviderInfo } from '../api/types';
import { h } from '../lib/dom';
import { icon } from '../lib/icons';
import { brandMark } from '../lib/logo';
import { isMac } from '../lib/shortcuts';
import { useAuditStore } from '../state/audit';
import { useUiStore } from '../state/ui';
import { createPackagePicker } from './packagePicker';
import { providerLabel } from './settingsDialog';

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
  const brand = h('div', { class: 'brand' }, brandMark(), h('span', { class: 'brand-name', text: 'aisai' }));

  // The project being inspected, always visible: name, version and where it came from.
  const projectLabel = h('span', { class: 'project-label', text: 'Inspecting' });
  const projectName = h('strong', { class: 'project-name', text: '' });
  const projectMeta = h('span', { class: 'project-meta', text: '' });
  const projectChip = h('div', { class: 'project-chip', hidden: true, role: 'status', 'aria-live': 'polite' }, projectLabel, projectName, projectMeta);
  projectChip.addEventListener('click', () => {
    if (projectChip.classList.contains('is-deleted')) document.getElementById('package-input')?.focus();
  });

  const scanButton = button('Pull', 'primary');
  scanButton.title = 'Download a package, or clone a repository, into this audit. It does not scan.';
  // data-tour marks the controls the tutorials point at (views/tutorials.ts).
  scanButton.dataset.tour = 'pull';
  const folderButton = button('Open folder…');
  folderButton.dataset.tour = 'open-folder';
  folderButton.title = `Inspect a project folder on this computer (${isMac() ? '⌘O' : 'Ctrl+O'}, or drop a folder on the window)`;
  const staticButton = button('Static scan');
  staticButton.title = 'Run the static rules over the pulled code';
  staticButton.dataset.tour = 'static-scan';
  staticButton.setAttribute('aria-keyshortcuts', isMac() ? 'Meta+Shift+S' : 'Control+Shift+S');
  const llmButton = button('LLM scan');
  llmButton.title = 'Ask the model to review the pulled code';
  llmButton.dataset.tour = 'llm-scan';

  const providerChip = h('span', { class: 'provider-chips' }, h('span', { class: 'badge neutral', text: 'checking provider…' }));
  const mockBadge = h('span', { class: 'badge mock', text: 'MOCK DATA', hidden: true });
  const terminalButton = h(
    'button',
    { class: 'ghost icon', type: 'button', title: 'Terminal (Ctrl+`)', 'aria-label': 'Terminal', 'aria-pressed': 'false' },
    icon('terminal'),
  );
  terminalButton.addEventListener('click', () => {
    const ui = useUiStore.getState();
    ui.setTerminalOpen(!ui.terminalOpen);
  });
  const settingsButton = h(
    'button',
    { class: 'ghost icon', type: 'button', title: `Settings (${isMac() ? '⌘,' : 'Ctrl+,'})`, 'aria-label': 'Settings' },
    icon('gear'),
  );
  settingsButton.dataset.tour = 'settings';
  terminalButton.dataset.tour = 'terminal';

  // The self-updater: shown only while there is something to say.
  const updateButton = h('button', { class: 'tiny update-button', type: 'button', hidden: true });
  let updateState: UpdateStatus = { state: 'idle' };
  updateButton.addEventListener('click', () => {
    if (updateState.state === 'ready') void window.aisai.installUpdate();
    else if (updateState.state === 'manual') void window.aisai.openReleasePage();
  });
  const unsubscribeUpdate = window.aisai.onUpdateStatus((status) => {
    updateState = status;
    const label: Partial<Record<UpdateStatus['state'], string>> = {
      downloading: `Downloading update ${status.version ?? ''} ${status.percent ?? 0}%`,
      ready: `Restart to update to ${status.version ?? 'the new version'}`,
      manual: `Update ${status.version ?? ''} available: download`,
    };
    const text = label[status.state];
    updateButton.hidden = !text;
    updateButton.textContent = text ?? '';
    updateButton.disabled = status.state === 'downloading';
    updateButton.classList.toggle('primary', status.state === 'ready');
    updateButton.title = status.message ?? 'AISAI updates itself in the background';
  });

  picker.onScan((packageName, version, ecosystem) => {
    void controller.startAudit(packageName, version, ecosystem);
  });

  picker.onPullLink((url) => {
    void controller.startLinkAudit(url);
  });

  scanButton.addEventListener('click', () => {
    const pkg = picker.packageName();
    if (!pkg) {
      picker.promptForName();
      return;
    }
    if (picker.repoUrl()) {
      void controller.startLinkAudit(pkg);
      return;
    }
    void controller.startAudit(pkg, picker.version(), picker.ecosystem());
  });

  staticButton.addEventListener('click', () => {
    void controller.runScan('sast');
  });

  llmButton.addEventListener('click', () => {
    void controller.runScan('llm');
  });

  folderButton.addEventListener('click', () => {
    void controller.chooseFolder();
  });

  // Ctrl/Cmd+O opens the folder dialog from anywhere in the window.
  // Dropping a folder on the window audits it.
  const hasFiles = (event: DragEvent): boolean => Boolean(event.dataTransfer?.types.includes('Files'));
  const onDragOver = (event: DragEvent): void => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    document.body.classList.add('drop-target');
  };
  const onDragLeave = (event: DragEvent): void => {
    // relatedTarget is null when the pointer leaves the window.
    if (!event.relatedTarget) document.body.classList.remove('drop-target');
  };
  const onDrop = (event: DragEvent): void => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    document.body.classList.remove('drop-target');
    const file = event.dataTransfer?.files[0];
    if (!file) return;
    void window.aisai
      .folderFromDrop(file)
      .then((folder) => {
        if (folder) {
          void controller.startFolderAudit(folder);
        } else {
          useAuditStore.getState().setError('Drop a project folder, not a file.');
        }
      })
      .catch((err: unknown) => {
        useAuditStore.getState().setError(err instanceof Error ? err.message : String(err));
      });
  };
  document.addEventListener('dragover', onDragOver);
  document.addEventListener('dragleave', onDragLeave);
  document.addEventListener('drop', onDrop);

  settingsButton.addEventListener('click', () => {
    useUiStore.getState().setSettingsOpen(true);
  });

  // Two groups: the controls, which wrap onto a second row when the window is
  // narrow, and the status and settings buttons, which always stay in view at
  // the right edge.
  container.append(
    h('div', { class: 'toolbar-main' }, brand, picker.element, scanButton, folderButton, staticButton, llmButton, projectChip),
    h('div', { class: 'toolbar-end' }, updateButton, mockBadge, providerChip, terminalButton, settingsButton),
  );

  const refreshProviders = async (): Promise<void> => {
    const providers = await controller.fetchProviders();
    providerChip.replaceChildren(...providerChips(providers));
    void controller.refreshChatModel(providers);
  };

  const showProject = (audit: ReturnType<typeof useAuditStore.getState>['current']): void => {
    projectChip.hidden = !audit;
    if (!audit) return;
    const from =
      audit.source === 'local'
        ? 'folder'
        : audit.source === 'git' || audit.source === 'repo'
          ? 'repository'
          : ({ npm: 'npm', cargo: 'crates.io', pypi: 'PyPI' } as Record<string, string>)[audit.ecosystem ?? ''] ?? 'PyPI';
    // Deleted sources leave nothing to inspect: say so and point to the way back.
    projectChip.classList.toggle('is-deleted', Boolean(audit.sourcesDeleted));
    projectLabel.textContent = audit.sourcesDeleted ? 'Sources deleted' : 'Inspecting';
    if (audit.sourcesDeleted) {
      projectName.textContent = audit.package;
      projectMeta.textContent = 'pull again to start';
      projectChip.title = `The downloaded sources of ${audit.package} were deleted. Pull a package or open a folder to start again.`;
      return;
    }
    projectName.textContent = audit.package;
    const nested = audit.skippedRepos?.length ?? 0;
    projectMeta.textContent = [
      audit.version && audit.version !== 'local' ? `v${audit.version}` : '',
      from,
      audit.truncated ? 'partial copy' : '',
      nested ? `${nested} nested repo${nested === 1 ? '' : 's'} skipped` : '',
    ].filter(Boolean).join(' · ');
    projectChip.classList.toggle('is-partial', Boolean(audit.truncated) || nested > 0);
    projectChip.title = `${audit.package}${audit.version ? ` ${audit.version}` : ''} (${from})${audit.fileCount ? ` · ${audit.fileCount} files` : ''}${audit.truncated ? ' · The folder is larger than the copy limit (20,000 files or 256 MB), so only part of it is audited.' : ''}${nested ? ` · Skipped nested repositories (worktrees, submodules or copies, audited separately if you open them): ${audit.skippedRepos?.join(', ')}` : ''}`;
  };

  const unsubscribeAudit = useAuditStore.subscribe((state) => {
    showProject(state.current);
    brand.classList.toggle('is-busy', state.busy !== null);
    setBusy(scanButton, state.busy === 'audit');
    folderButton.disabled = state.busy === 'audit';
    // A scan needs something pulled, and only one job runs at a time.
    const ready = state.current?.status === 'ready' && !state.current.sourcesDeleted;
    setBusy(staticButton, state.busy === 'scan:sast');
    setBusy(llmButton, state.busy === 'scan:llm');
    staticButton.disabled = !ready || state.busy !== null;
    llmButton.disabled = !ready || state.busy !== null;
    const why = ready ? '' : 'Pull a package or open a folder first';
    staticButton.title = why || 'Run the static rules over the pulled code';
    llmButton.title = why || 'Ask the model to review the pulled code';
  });

  showProject(useAuditStore.getState().current);

  const unsubscribeUi = useUiStore.subscribe((state, previous) => {
    if (state.providersStamp !== previous.providersStamp) void refreshProviders();
    if (state.terminalOpen !== previous.terminalOpen) {
      terminalButton.setAttribute('aria-pressed', String(state.terminalOpen));
      terminalButton.classList.toggle('is-on', state.terminalOpen);
    }
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
      unsubscribeUi();
      unsubscribeBackend();
      unsubscribeUpdate();
      document.removeEventListener('dragover', onDragOver);
      document.removeEventListener('dragleave', onDragLeave);
      document.removeEventListener('drop', onDrop);
      document.body.classList.remove('drop-target');
      picker.dispose();
    },
  };
}

function providerChips(providers: ProviderInfo[]): Node[] {
  if (!providers.length) {
    return [h('span', { class: 'badge provider-badge is-bad', role: 'button', tabindex: '0', title: 'Choose a model server', onclick: () => {
      useUiStore.getState().setSettingsSection('server');
      useUiStore.getState().setSettingsOpen(true);
    } }, h('span', { class: 'status-dot', 'aria-hidden': 'true' }), 'No model server')];
  }
  return providers.map((provider) =>
    h(
      'span',
      {
        class: `badge provider-badge ${provider.ok ? 'is-ok' : 'is-bad'}`,
        title: (provider.error ?? `${provider.id} (${provider.kind})`) + ' · click to configure',
        role: 'button',
        tabindex: '0',
        onclick: () => {
          useUiStore.getState().setSettingsSection('server');
          useUiStore.getState().setSettingsOpen(true);
        },
      },
      h('span', { class: 'status-dot', 'aria-hidden': 'true' }),
      `${providerLabel(provider.kind)}${provider.ok ? '' : ' offline'}`,
    ),
  );
}
