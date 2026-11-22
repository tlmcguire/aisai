import type { AppController } from '../app';
import type { Ecosystem, LinkInfo } from '../api/types';
import { h } from '../lib/dom';
import { useAuditStore } from '../state/audit';

/**
 * Packages that are worth offering before anything has been audited.
 *
 * The package index has no search endpoint, so the suggestions come from
 * two places: the packages that were audited before, and a short list of
 * the packages people actually install.
 */
const POPULAR_PACKAGES: Record<Ecosystem, string[]> = {
  pypi: [
    'requests', 'urllib3', 'flask', 'django', 'numpy', 'pandas', 'scipy', 'boto3', 'pyyaml', 'jinja2',
    'click', 'setuptools', 'pytest', 'cryptography', 'pillow', 'sqlalchemy', 'aiohttp', 'fastapi',
    'pydantic', 'rich',
  ],
  npm: [
    'express', 'react', 'lodash', 'axios', 'webpack', 'typescript', 'next', 'vue', 'moment', 'chalk',
    'commander', 'debug', 'uuid', 'jsonwebtoken', 'minimist', 'yargs', 'eslint', 'jest', 'body-parser',
    'node-fetch',
  ],
  cargo: [
    'serde', 'tokio', 'rand', 'clap', 'regex', 'reqwest', 'hyper', 'syn', 'anyhow', 'log',
    'chrono', 'once_cell', 'itertools', 'bytes', 'futures', 'axum', 'tracing', 'thiserror', 'openssl', 'rustls',
  ],
};

/** The package managers a scan can start from. */
export const ECOSYSTEMS: Array<{ id: Ecosystem; label: string; example: string }> = [
  { id: 'pypi', label: 'PyPI (pip)', example: 'flask' },
  { id: 'npm', label: 'npm', example: 'express' },
  { id: 'cargo', label: 'crates.io (cargo)', example: 'serde' },
];

/** How long to wait before asking the index about the typed name. */
const LOOKUP_DELAY_MS = 250;

/** The largest number of suggestions shown at once. */
const MAX_SUGGESTIONS = 8;

/** A package that can be audited. */
interface Suggestion {
  /** The name of the package. */
  name: string;
  /** The version it was last audited at, when it is a known package. */
  version: string;
  /** Whether the package was audited before. */
  recent: boolean;
}

export interface PackagePicker {
  /** The elements to append to the toolbar. */
  element: DocumentFragment;
  /** The package name to audit. */
  packageName(): string;
  /** The package manager the name belongs to. */
  ecosystem(): Ecosystem;
  /** The pasted repository link, when the field holds one; null for a package. */
  repoUrl(): string | null;
  /** Registers the handler called when a pasted link is to be pulled. */
  onPullLink(handler: (url: string) => void): void;
  /** The version to audit; an empty string means the latest release. */
  version(): string;
  /** Registers the handler called when the user asks to scan. */
  onScan(handler: (packageName: string, version: string, ecosystem: Ecosystem) => void): void;
  /** Focuses the package field. */
  focus(): void;
  /** Focuses the field and explains that a package name is needed. */
  promptForName(): void;
  /** Releases the listeners and timers. */
  dispose(): void;
}

/**
 * Builds the package picker: a combobox for the package name and a dropdown
 * for the version.
 *
 * The version is never typed by hand. It is either "latest" or one of the
 * versions the index actually published, which removes the most common way
 * of auditing the wrong release.
 */
export function createPackagePicker(controller: AppController): PackagePicker {
  const input = h('input', {
    type: 'text',
    id: 'package-input',
    placeholder: 'flask, or paste a link',
    role: 'combobox',
    'aria-expanded': 'false',
    'aria-autocomplete': 'list',
    'aria-controls': 'package-suggestions',
    'aria-label': 'Package name',
    autocomplete: 'off',
    spellcheck: 'false',
  });

  const list = h('ul', {
    id: 'package-suggestions',
    class: 'picker-list',
    role: 'listbox',
    'aria-label': 'Package suggestions',
    hidden: true,
  });

  const status = h('span', { class: 'picker-status', role: 'status', 'aria-live': 'polite' });

  const versionSelect = h(
    'select',
    { id: 'version-input', 'aria-label': 'Package version' },
    h('option', { value: '', text: 'latest' }),
  );

  const ecosystemSelect = h(
    'select',
    { id: 'ecosystem-input', 'aria-label': 'Package manager' },
    ...ECOSYSTEMS.map((eco) => h('option', { value: eco.id, text: eco.label })),
  );

  const element = document.createDocumentFragment();
  element.append(
    h(
      'div',
      { class: 'field picker-ecosystem-field' },
      h('label', { for: 'ecosystem-input', text: 'Source' }),
      ecosystemSelect,
    ),
    h(
      'div',
      { class: 'picker-field' },
      h(
        'div',
        { class: 'field' },
        h('label', { for: 'package-input', text: 'Package or link' }),
        // The status sits inside the field, at its right edge, so the
        // toolbar never changes height while the name is looked up.
        h('div', { class: 'picker' }, input, status, list),
      ),
    ),
    h(
      'div',
      { class: 'field picker-version-field' },
      h('label', { for: 'version-input', text: 'Version' }),
      versionSelect,
    ),
  );

  let linkHandler: ((url: string) => void) | null = null;
  /** Set while the field holds a repository link. */
  let repo: LinkInfo | null = null;
  let linkToken = 0;
  let scanHandler: ((packageName: string, version: string, ecosystem: Ecosystem) => void) | null = null;
  const currentEcosystem = (): Ecosystem => ecosystemSelect.value as Ecosystem;
  let suggestions: Suggestion[] = [];
  let activeIndex = -1;
  let lookupTimer: ReturnType<typeof setTimeout> | null = null;
  let lookupToken = 0;

  const closeList = (): void => {
    list.hidden = true;
    input.setAttribute('aria-expanded', 'false');
    activeIndex = -1;
  };

  const setStatus = (text: string, title = ''): void => {
    status.textContent = text;
    status.title = title;
    status.classList.toggle('is-error', !text && Boolean(title));
    // The status cannot take the pointer (it sits over the input), so its
    // detail moves to the input's own tooltip.
    input.title = title || text;
    // Keep the typed text clear of the status.
    requestAnimationFrame(() => {
      input.style.paddingRight = status.textContent ? `${status.offsetWidth + 14}px` : '';
    });
  };

  const renderSuggestions = (items: Suggestion[]): void => {
    suggestions = items;
    activeIndex = -1;
    if (!items.length) {
      closeList();
      return;
    }
    list.replaceChildren(
      ...items.map((item, index) =>
        h(
          'li',
          {
            class: `picker-option${item.recent ? ' is-recent' : ''}`,
            role: 'option',
            id: `package-option-${index}`,
            'aria-selected': 'false',
            // mousedown rather than click: the input keeps the focus, so the
            // list does not close before the option is picked.
            onmousedown: (event: Event) => {
              event.preventDefault();
              choose(item);
            },
          },
          h('span', { class: 'picker-option-name', text: item.name }),
          item.version
            ? h('span', { class: 'picker-option-version', text: item.version })
            : null,
        ),
      ),
    );
    list.hidden = false;
    input.setAttribute('aria-expanded', 'true');
  };

  const highlight = (index: number): void => {
    const options = Array.from(list.querySelectorAll<HTMLElement>('.picker-option'));
    options.forEach((option, i) => {
      const active = i === index;
      option.classList.toggle('is-active', active);
      option.setAttribute('aria-selected', String(active));
    });
    if (index >= 0) {
      input.setAttribute('aria-activedescendant', `package-option-${index}`);
    } else {
      input.removeAttribute('aria-activedescendant');
    }
    activeIndex = index;
  };

  const populateVersions = (versions: string[]): void => {
    versionSelect.replaceChildren(
      h('option', { value: '', text: 'latest' }),
      ...versions.map((version) => h('option', { value: version, text: version })),
    );
  };

  const resolve = async (name: string): Promise<void> => {
    const token = ++lookupToken;
    if (!name) {
      setStatus('');
      populateVersions([]);
      return;
    }
    const lookup = await controller.lookupPackage(name, currentEcosystem());
    if (token !== lookupToken) return; // a newer lookup won
    if (!lookup) {
      setStatus('', 'the package registry is unreachable');
      return;
    }
    if (lookup.error) {
      setStatus(`${lookup.name} was not found`, lookup.error);
      populateVersions([]);
      return;
    }
    const title = lookup.summary ?? '';
    setStatus(lookup.latest ? `${lookup.name} ${lookup.latest}` : lookup.name, title);
    populateVersions(lookup.versions ?? []);
  };

  const scheduleResolve = (): void => {
    if (lookupTimer) clearTimeout(lookupTimer);
    lookupTimer = setTimeout(() => {
      lookupTimer = null;
      void resolve(input.value.trim());
    }, LOOKUP_DELAY_MS);
  };

  const looksLikeLink = (text: string): boolean => /^(https?:\/\/|git@|pkg:)/i.test(text.trim());

  /** A pasted link fills in the fields: a package page sets the source, name and version. */
  const resolveLink = async (text: string): Promise<void> => {
    const token = ++linkToken;
    setStatus('Reading the link…');
    const result = await controller.resolveLink(text.trim());
    if (token !== linkToken) return;
    if (!result.link) {
      repo = null;
      ecosystemSelect.disabled = false;
      versionSelect.disabled = false;
      setStatus('', result.error ?? 'that link is not a package page or a repository');
      status.textContent = 'Not a package or repository link';
      return;
    }
    const link = result.link;
    if (link.kind === 'repo') {
      repo = link;
      ecosystemSelect.disabled = true;
      versionSelect.disabled = true;
      populateVersions([]);
      setStatus(`Repository ${link.display ?? link.name}${link.ref ? ` @ ${link.ref}` : ''}`, link.repoUrl ?? '');
      return;
    }
    repo = null;
    ecosystemSelect.disabled = false;
    versionSelect.disabled = false;
    if (link.ecosystem) ecosystemSelect.value = link.ecosystem;
    input.value = link.name ?? '';
    await resolve(input.value.trim());
    if (token !== linkToken) return;
    if (link.version) {
      if (!Array.from(versionSelect.options).some((o) => o.value === link.version)) {
        versionSelect.append(h('option', { value: link.version, text: link.version }));
      }
      versionSelect.value = link.version;
    }
  };

  const choose = (item: Suggestion): void => {
    input.value = item.name;
    closeList();
    void resolve(item.name);
  };

  const knownPackages = (): Suggestion[] => {
    const seen = new Set<string>();
    const out: Suggestion[] = [];
    for (const audit of useAuditStore.getState().audits) {
      // A folder is not a package name: it cannot be looked up on the index.
      if (audit.source === 'local') continue;
      if ((audit.ecosystem ?? 'pypi') !== currentEcosystem()) continue;
      const key = audit.package.toLowerCase();
      if (!key || seen.has(key)) continue;
      seen.add(key);
      out.push({ name: audit.package, version: audit.version ?? '', recent: true });
    }
    return out;
  };

  const updateSuggestions = (): void => {
    const query = input.value.trim().toLowerCase();
    const recent = knownPackages();
    const popular: Suggestion[] = POPULAR_PACKAGES[currentEcosystem()].map((name) => ({
      name,
      version: '',
      recent: false,
    }));

    // A package that was audited before is offered once, with the version
    // it was audited at.
    const recentNames = new Set(recent.map((item) => item.name.toLowerCase()));
    const fresh = popular.filter((item) => !recentNames.has(item.name.toLowerCase()));

    let items: Suggestion[];
    if (!query) {
      items = [...recent, ...fresh];
    } else {
      const starts = fresh.filter((item) => item.name.toLowerCase().startsWith(query));
      const contains = fresh.filter(
        (item) =>
          !item.name.toLowerCase().startsWith(query) &&
          item.name.toLowerCase().includes(query),
      );
      const matchedRecent = recent.filter((item) => item.name.toLowerCase().includes(query));
      items = [...matchedRecent, ...starts, ...contains];
    }
    renderSuggestions(items.slice(0, MAX_SUGGESTIONS));
  };

  // An empty Scan used to only move the focus, which read as a dead button.
  // Now it says what is missing and nudges the field.
  const promptForName = (): void => {
    input.focus();
    setStatus('Type a package name or paste a link, then Pull');
    input.classList.remove('is-invalid');
    void input.offsetWidth;
    input.classList.add('is-invalid');
  };

  input.addEventListener('animationend', () => input.classList.remove('is-invalid'));

  const submit = (): void => {
    const name = input.value.trim();
    if (!name) {
      promptForName();
      return;
    }
    closeList();
    if (repo) {
      linkHandler?.(name);
      return;
    }
    scanHandler?.(name, versionSelect.value, currentEcosystem());
  };

  input.addEventListener('input', () => {
    if (looksLikeLink(input.value)) {
      closeList();
      if (lookupTimer) clearTimeout(lookupTimer);
      lookupTimer = setTimeout(() => void resolveLink(input.value), LOOKUP_DELAY_MS);
      return;
    }
    if (repo) {
      repo = null;
      ecosystemSelect.disabled = false;
      versionSelect.disabled = false;
    }
    updateSuggestions();
    scheduleResolve();
  });

  input.addEventListener('focus', () => {
    updateSuggestions();
  });

  input.addEventListener('blur', () => {
    // The list is closed on the next tick so a click on an option still
    // reaches it.
    setTimeout(closeList, 120);
  });

  input.addEventListener('keydown', (event: KeyboardEvent) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      if (list.hidden || !suggestions.length) return;
      event.preventDefault();
      const delta = event.key === 'ArrowDown' ? 1 : -1;
      const next = activeIndex + delta;
      highlight(next < 0 ? suggestions.length - 1 : next % suggestions.length);
      return;
    }
    if (event.key === 'Escape') {
      if (!list.hidden) {
        event.preventDefault();
        closeList();
      }
      return;
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      if (activeIndex >= 0 && suggestions[activeIndex]) {
        choose(suggestions[activeIndex]);
        return;
      }
      submit();
    }
  });

  const syncEcosystem = (): void => {
    const eco = ECOSYSTEMS.find((candidate) => candidate.id === currentEcosystem()) ?? ECOSYSTEMS[0];
    input.placeholder = eco.example;
    populateVersions([]);
    setStatus('');
    closeList();
    if (input.value.trim()) void resolve(input.value.trim());
  };
  ecosystemSelect.addEventListener('change', syncEcosystem);

  versionSelect.addEventListener('change', () => {
    const value = versionSelect.value;
    const name = input.value.trim();
    setStatus(value ? `${name} ${value}` : name ? `${name} (latest)` : '');
  });

  return {
    element,
    packageName: () => input.value.trim(),
    ecosystem: currentEcosystem,
    repoUrl: () => (repo ? input.value.trim() : null),
    onPullLink: (handler) => {
      linkHandler = handler;
    },
    version: () => versionSelect.value,
    onScan: (handler) => {
      scanHandler = handler;
    },
    focus: () => input.focus(),
    promptForName,
    dispose: () => {
      if (lookupTimer) clearTimeout(lookupTimer);
      lookupTimer = null;
      scanHandler = null;
    },
  };
}
