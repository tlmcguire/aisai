// @vitest-environment jsdom
/**
 * The files of a project are told apart by the logo of their language.
 *
 * The marks are the file icons of an editor, so a Python file, a Rust file
 * and a Markdown file are known at a glance instead of by their extension.
 */

import { describe, expect, it } from 'vitest';

import { fileSymbol, isScanned, languageOf } from '../src/lib/langicons';

describe('the language of a file', () => {
  it('is known by the extension', () => {
    expect(languageOf('app.py')).toBe('python');
    expect(languageOf('main.rs')).toBe('rust');
    expect(languageOf('Program.cs')).toBe('csharp');
    expect(languageOf('README.md')).toBe('markdown');
    expect(languageOf('main.cpp')).toBe('cpp');
  });

  it('is known for the files that are named rather than extended', () => {
    expect(languageOf('Dockerfile')).toBe('docker');
    expect(languageOf('Cargo.toml')).toBe('rust');
    expect(languageOf('.env')).toBe('env');
  });

  it('is unknown for a file AISAI has never heard of', () => {
    expect(languageOf('mystery.qqq')).toBeUndefined();
  });

  it('is known for the languages AISAI scans', () => {
    expect(isScanned('app.py')).toBe(true);
    expect(isScanned('notes.txt')).toBe(false);
  });
});

describe('the symbol of a file', () => {
  it('is the logo of the language', () => {
    const symbol = fileSymbol('app.py', true);
    expect(symbol).not.toBeNull();
    expect(symbol?.tagName.toLowerCase()).toBe('svg');
    expect(symbol?.getAttribute('viewBox')).toBeTruthy();
    expect(symbol?.querySelectorAll('path').length).toBeGreaterThan(0);
  });

  it('is dimmed for a language AISAI cannot scan', () => {
    expect(fileSymbol('notes.txt', false)?.getAttribute('class')).toBe('file-symbol is-plain');
    expect(fileSymbol('app.py', true)?.getAttribute('class')).toBe('file-symbol');
  });

  it('is nothing for a file AISAI has never heard of', () => {
    expect(fileSymbol('mystery.qqq', true)).toBeNull();
  });
});
