import ts from 'typescript';
import { readdirSync, readFileSync, lstatSync } from 'node:fs';
import { resolve, relative, sep, extname } from 'node:path';

const ignored = new Set(['node_modules', '.git', '.next', '.jase-tunnel', 'dist', 'build', 'coverage', 'vendor', 'data', 'tests', 'test', '__tests__', 'fixtures']);
const own = n => n.modifiers?.some(m => m.kind === ts.SyntaxKind.ExportKeyword);
const propName = n => n && (ts.isIdentifier(n) || ts.isStringLiteral(n) ? n.text : undefined);
const unwrap = n => n && (ts.isAsExpression(n) || ts.isSatisfiesExpression(n) || ts.isParenthesizedExpression(n) ? unwrap(n.expression) : n);
const property = (node, key) => ts.isObjectLiteralExpression(node) ? node.properties.find(p => propName(p.name) === key) : undefined;

export function discoverTools(project) {
  const root = resolve(project), found = [], warnings = [];
  let files = 0, bytes = 0, directories = 0;
  const walk = (directory,depth=0) => {
    if (++directories>5000 || depth>24) throw new Error('Project too large for automatic scanning. Use tools add.');
    for (const entry of readdirSync(directory, { withFileTypes: true }).sort((a,b) => a.name.localeCompare(b.name))) {
      if (entry.isSymbolicLink() || entry.name.startsWith('.') || ignored.has(entry.name)) continue;
      const path = resolve(directory, entry.name);
      if (entry.isDirectory()) { walk(path,depth+1); continue; }
      if (!/\.(?:[cm]?[jt]s|[jt]sx)$/.test(entry.name) || /(?:\.d\.[cm]?ts|\.(?:test|spec)\.[jt]sx?)$/.test(entry.name) || entry.name.startsWith('jase-tunnel.')) continue;
      if (++files > 5000) throw new Error('Scan stopped: more than 5000 source files. Use tools add for the required modules.');
      const size=lstatSync(path).size;
      if (size > 1024 * 1024) { warnings.push(`${relative(root,path)}: file larger than 1 MiB skipped.`); continue; }
      bytes+=size;if(bytes>32*1024*1024)throw new Error('Source files exceed 32 MiB. Use tools add for the required modules.');
      const source = ts.createSourceFile(path, readFileSync(path, 'utf8'), ts.ScriptTarget.Latest, true, /x$/.test(extname(path)) ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
      const factories = new Set(), declarations = new Map(), exports = new Map();
      for (const statement of source.statements) {
        if (ts.isImportDeclaration(statement) && ts.isStringLiteral(statement.moduleSpecifier)) {
          const module = statement.moduleSpecifier.text;
          if (['ai', '@langchain/core/tools', 'jase-tunnel/server'].includes(module)) for (const item of statement.importClause?.namedBindings?.elements ?? []) if (['tool', 'defineTool'].includes(item.propertyName?.text ?? item.name.text)) factories.add(item.name.text);
        }
        if (ts.isVariableStatement(statement)) for (const declaration of statement.declarationList.declarations) if (ts.isIdentifier(declaration.name) && declaration.initializer) {
          declarations.set(declaration.name.text, declaration.initializer);
          if (own(statement)) exports.set(declaration.name.text, declaration.name.text);
        }
        if (ts.isExportDeclaration(statement) && !statement.moduleSpecifier) for (const item of statement.exportClause?.elements ?? []) exports.set(item.name.text, item.propertyName?.text ?? item.name.text);
      }
      const resolveNode = (n, seen = new Set()) => {
        n = unwrap(n);
        if (n && ts.isIdentifier(n) && declarations.has(n.text) && !seen.has(n.text)) return resolveNode(declarations.get(n.text), new Set([...seen,n.text]));
        return n;
      };
      const inspect = (node, exported, pathParts = [], depth = 0) => {
        if (depth > 5) return;
        node = resolveNode(node); if (!node) return;
        let shape = node, kind = 'declaration';
        if (ts.isCallExpression(node)) {
          if (!ts.isIdentifier(node.expression) || !factories.has(node.expression.text)) return;
          kind = node.expression.text;
          shape = resolveNode(node.arguments[0]);
          if ((!shape || !ts.isObjectLiteralExpression(shape)) && node.arguments[1]) shape = resolveNode(node.arguments[1]);
          if (!shape || !ts.isObjectLiteralExpression(shape)) return;
          // LangChain tool(fn, {name, schema, description}).
          if (!property(shape, 'execute') && !property(shape, 'parameters') && node.arguments[1]) shape = resolveNode(node.arguments[1]);
        }
        if (shape && ts.isObjectLiteralExpression(shape)) {
          const schema = property(shape, 'parameters') ?? property(shape, 'inputSchema') ?? property(shape, 'schema');
          const executable = property(shape, 'execute') ?? property(shape, 'invoke');
          if (schema && (executable || ts.isCallExpression(node) && node.arguments.length > 1)) {
            const named = property(shape,'name'), description = property(shape,'description');
            const nameNode = named && resolveNode(named.initializer);
            const fallback = pathParts.at(-1) ?? exported;
            const name = nameNode && ts.isStringLiteral(nameNode) ? nameNode.text : fallback.replace(/([a-z])([A-Z])/g,'$1_$2').toLowerCase().replace(/[^a-z0-9_]/g,'_');
            if (!/^[a-z][a-z0-9_]{1,40}$/.test(name)) { warnings.push(`${relative(root,path)}#${exported}: unrecognized tool name.`); return; }
            if (!description) { warnings.push(`${relative(root,path)}#${exported}: missing description.`); return; }
            // Found is not enabled: the owner decides which tools visitors may call.
            found.push({ name, file: relative(root,path).split(sep).join('/'), export: exported, path: pathParts, kind, authorize: !!property(shape,'authorize'), enabled: false }); return;
          }
          // Inspect only exported tool collections, never arbitrary application objects.
          if (/tools$/i.test(exported)) for (const p of shape.properties) {
            const key = propName(p.name);
            if (key && !['__proto__','constructor','prototype'].includes(key)) inspect(p.initializer ?? (ts.isShorthandPropertyAssignment(p) ? p.name : undefined),exported,[...pathParts,key],depth+1);
          }
        } else if (shape && ts.isArrayLiteralExpression(shape) && /tools$/i.test(exported)) shape.elements.forEach((item,index)=>inspect(item,exported,[...pathParts,String(index)],depth+1));
      };
      for (const [name, local] of exports) inspect(declarations.get(local), name);
    }
  };
  walk(root);
  const names = new Map();
  for (const tool of found) {
    if (names.has(tool.name)) throw new Error(`Duplicate name ${tool.name}: ${names.get(tool.name)} and ${tool.file}. Rename a tool or add it with --name.`);
    names.set(tool.name,tool.file);
  }
  return { tools: found, warnings, files };
}
