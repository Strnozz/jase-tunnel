import Ajv from 'ajv';
import Ajv2020 from 'ajv/dist/2020.js';
import { toJSONSchema } from 'zod';

const validationOptions = { strict: false, allErrors: false, coerceTypes: false, removeAdditional: false, useDefaults: false, addUsedSchema: false };
const ajv = new Ajv(validationOptions);
const ajv2020 = new Ajv2020(validationOptions);
const namePattern = /^[a-z][a-z0-9_]{1,40}$/;
export function defineTool(tool) { return tool; }

// These are project-owned objects, imported by its server. Discovery never imports them.
export function adaptTool(value, options = {}) {
  if (!value) throw new Error('Missing tool.');
  const name = options.name ?? value.name;
  if (!namePattern.test(name ?? '')) throw new Error(`Invalid tool name: ${name}`);
  const input = options.parameters ?? value.parameters ?? value.inputSchema ?? value.schema;
  let parameters;
  if (input?._zod) parameters = toJSONSchema(input, { target: 'draft-07', io: 'input', unrepresentable: 'throw' });
  else parameters = input?.jsonSchema ?? input;
  if (!parameters || typeof parameters !== 'object' || parameters.type !== 'object') throw new Error(`${name}: a JSON object schema or Zod 4 schema is required.`);
  if (JSON.stringify(parameters).length > 32_000) throw new Error(`${name}: schema too large.`);
  // Remote references are never fetched; Ajv refuses unresolved references.
  const validate = (parameters.$schema === 'https://json-schema.org/draft/2020-12/schema' ? ajv2020 : ajv).compile(parameters);
  const description = options.description ?? value.description;
  if (typeof description !== 'string' || !description.trim() || description.length > 4000) throw new Error(`${name}: description missing or too long.`);
  const execute = typeof value === 'function' ? value : value.execute ?? value.invoke;
  if (typeof execute !== 'function') throw new Error(`${name}: missing executor.`);
  const readOnly = options.readOnly === true || value.readOnly === true || value.riskLevel === 'read' || value.annotations?.readOnlyHint === true;
  // The model runs on the visitor's computer, so the visitor decides every tool call and
  // its arguments. A tool is reachable only when it checks the session itself
  // (authorize) or its owner declared it safe for any visitor (public).
  const isPublic = options.public === true || value.public === true;
  if (typeof value.authorize !== 'function' && !isPublic) {
    throw new Error(`${name}: any visitor can call this tool with arbitrary arguments. Add authorize(context, args), or declare public: true if it is safe for everyone.`);
  }
  return {
    name, description, parameters, readOnly, public: isPublic,
    async execute(args, context) {
      if (!validate(args)) throw new Error('Invalid tool arguments.');
      context.signal.throwIfAborted();
      if (typeof value.authorize === 'function' && await value.authorize(context, args) !== true) throw new Error('Tool not authorized for this session.');
      // Existing executors retain their validation/auth checks and receive this request.
      if (typeof input?.parseAsync === 'function') args = await input.parseAsync(args);
      const result = await execute.call(value, args, { ...context, abortSignal: context.signal, toolCallId: context.callId, messages: [] });
      if (result && typeof result[Symbol.asyncIterator] === 'function') throw new Error('Streaming tools require an adapter.');
      return result;
    }
  };
}

export function toolList(values) {
  const tools = values.map(v => adaptTool(v.tool ?? v, v.options ?? {}));
  if (tools.length > 100 || new Set(tools.map(t => t.name)).size !== tools.length) throw new Error('Duplicate tools or more than 100 tools.');
  return tools;
}
