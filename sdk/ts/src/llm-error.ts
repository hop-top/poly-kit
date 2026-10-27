/**
 * Base error of the llm layer. Lives apart from `llm.ts` so modules
 * that `llm.ts` re-exports can extend it without an import cycle.
 * Import it from `@hop-top/kit/llm`.
 *
 * @internal
 */
export class LLMError extends Error {
  statusCode?: number;
  code?: string;

  constructor(message: string) {
    super(message);
    this.name = "LLMError";
    Object.setPrototypeOf(this, new.target.prototype);
  }
}
