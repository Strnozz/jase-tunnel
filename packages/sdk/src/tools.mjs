import Ajv from 'ajv';
import { toJSONSchema } from 'zod';

const ajv = new Ajv({ strict: false, allErrors: false, coerceTypes: false, removeAdditional: false, useDefaults: false });
const namePattern = /^[a-z][a-z0-9_]{1,40}$/;
export function defineTool(tool) { return tool; }

// These are project-owned objects, imported by its server. Discovery never imports them.
export function adaptTool(value, options = {}) {
  if (!value) throw new Error('Strumento mancante.');
  const name = options.name ?? value.name;
  if (!namePattern.test(name ?? '')) throw new Error(`Nome strumento non valido: ${name}`);
  const input = options.parameters ?? value.parameters ?? value.inputSchema ?? value.schema;
  let parameters;
  if (input?._zod) parameters = toJSONSchema(input, { target: 'draft-07', io: 'input', unrepresentable: 'throw' });
  else parameters = input?.jsonSchema ?? input;
  if (!parameters || typeof parameters !== 'object' || parameters.type !== 'object') throw new Error(`${name}: serve uno schema JSON oggetto o Zod 4.`);
  if (JSON.stringify(parameters).length > 32_000) throw new Error(`${name}: schema troppo grande.`);
  // Remote references are never fetched; Ajv refuses unresolved references.
  const validate = ajv.compile(parameters);
  const description = options.description ?? value.description;
  if (typeof description !== 'string' || !description.trim() || description.length > 4000) throw new Error(`${name}: descrizione mancante o troppo lunga.`);
  const execute = typeof value === 'function' ? value : value.execute ?? value.invoke;
  if (typeof execute !== 'function') throw new Error(`${name}: esecutore mancante.`);
  const readOnly = options.readOnly === true || value.readOnly === true || value.riskLevel === 'read' || value.annotations?.readOnlyHint === true;
  return {
    name, description, parameters, readOnly,
    async execute(args, context) {
      if (!validate(args)) throw new Error('Argomenti dello strumento non validi.');
      context.signal.throwIfAborted();
      if (typeof value.authorize === 'function' && await value.authorize(context, args) !== true) throw new Error('Strumento non autorizzato per questa sessione.');
      // Existing executors retain their validation/auth checks and receive this request.
      if (typeof input?.parseAsync === 'function') args = await input.parseAsync(args);
      const result = await execute.call(value, args, { ...context, abortSignal: context.signal, toolCallId: context.callId, messages: [] });
      if (result && typeof result[Symbol.asyncIterator] === 'function') throw new Error('Gli strumenti streaming richiedono un adattatore.');
      return result;
    }
  };
}

export function toolList(values) {
  const tools = values.map(v => adaptTool(v.tool ?? v, v.options ?? {}));
  if (tools.length > 100 || new Set(tools.map(t => t.name)).size !== tools.length) throw new Error('Strumenti duplicati o più di 100 strumenti.');
  return tools;
}
