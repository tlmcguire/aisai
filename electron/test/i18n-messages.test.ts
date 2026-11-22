/**
 * A message is never glued to other text.
 *
 * Languages put their words in a different order, so a sentence that is put
 * together in code cannot be put back together in another language. The
 * interface therefore keeps the key of every message and makes it again
 * when the language changes, and that only works as long as no message is
 * turned into plain text on the way.
 *
 * This test reads the sources and looks for the places where that would
 * happen: a message inside a template, a message added to another text, a
 * message used as the sign of a join, or a message turned into a string.
 */

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import * as ts from 'typescript';
import { describe, expect, it } from 'vitest';

const sourceRoot = fileURLToPath(new URL('../src', import.meta.url));

function sourceFiles(dir: string): string[] {
  const found: string[] = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) found.push(...sourceFiles(path));
    else if (path.endsWith('.ts') && !path.endsWith('.d.ts')) found.push(path);
  }
  return found;
}

/** Whether the node is a call to t() or tn(). */
function isMessageCall(node: ts.Node): boolean {
  return (
    ts.isCallExpression(node) &&
    ts.isIdentifier(node.expression) &&
    (node.expression.text === 't' || node.expression.text === 'tn')
  );
}

/** Whether the node holds a message anywhere inside it. */
function holdsMessage(node: ts.Node): boolean {
  if (isMessageCall(node)) return true;
  let found = false;
  const visit = (child: ts.Node): void => {
    if (found) return;
    if (isMessageCall(child)) {
      found = true;
      return;
    }
    ts.forEachChild(child, visit);
  };
  ts.forEachChild(node, visit);
  return found;
}

function violationsIn(source: ts.SourceFile): string[] {
  const found: string[] = [];
  const line = (node: ts.Node): number => source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1;

  const visit = (node: ts.Node, parent?: ts.Node): void => {
    if (ts.isTemplateExpression(node)) {
      // A template that is the key of a message is not text: the key is
      // looked up, not shown.
      const isKey =
        parent !== undefined &&
        ts.isCallExpression(parent) &&
        isMessageCall(parent) &&
        parent.arguments[0] === node;
      if (!isKey && node.templateSpans.some((span) => holdsMessage(span.expression))) {
        found.push(`line ${line(node)}: a message is put into other text`);
      }
    }
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      if (holdsMessage(node.left) || holdsMessage(node.right)) {
        found.push(`line ${line(node)}: a message is added to other text`);
      }
    }
    if (
      ts.isCallExpression(node) &&
      ts.isPropertyAccessExpression(node.expression) &&
      node.expression.name.text === 'join'
    ) {
      const sign = node.arguments.some((argument) => holdsMessage(argument));
      const list = node.expression.expression;
      const inline = ts.isArrayLiteralExpression(list) && holdsMessage(list);
      if (sign || inline) {
        found.push(`line ${line(node)}: messages are joined into plain text`);
      }
    }
    if (
      ts.isCallExpression(node) &&
      ts.isIdentifier(node.expression) &&
      node.expression.text === 'String' &&
      node.arguments.some((argument) => holdsMessage(argument))
    ) {
      found.push(`line ${line(node)}: a message is turned into plain text`);
    }
    ts.forEachChild(node, (child) => visit(child, node));
  };

  ts.forEachChild(source, (child) => visit(child));
  return found;
}

describe('the messages of the interface', () => {
  it('are never glued to other text', () => {
    const problems: string[] = [];
    for (const file of sourceFiles(sourceRoot)) {
      const source = ts.createSourceFile(file, readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true);
      const name = file.slice(sourceRoot.length + 1);
      for (const problem of violationsIn(source)) problems.push(`${name} ${problem}`);
    }
    expect(problems).toEqual([]);
  });
});
