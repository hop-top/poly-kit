/**
 * Headless sequential wizard engine — TypeScript port of kit/wizard.
 */

// --- enums ---

export enum StepKind {
  TextInput = "text_input",
  Select = "select",
  Confirm = "confirm",
  MultiSelect = "multi_select",
  Action = "action",
  Summary = "summary",
}

export enum ErrorAction {
  Abort = 0,
  Retry = 1,
  Skip = 2,
}

// --- types ---

export interface Option {
  value: string;
  label: string;
  description?: string;
}

export interface Condition {
  key: string;
  pred: (value: unknown) => boolean;
}

export type ActionFn = (
  results: Record<string, unknown>,
) => void | Promise<void>;

export type ValidateTextFn = (value: string) => void;
export type ValidateChoiceFn = (value: string) => void;
export type ValidateChoicesFn = (values: string[]) => void;
export type FormatFn = (results: Record<string, unknown>) => string;

// --- Step ---

export class Step {
  key: string;
  kind: StepKind;
  label: string;
  description?: string;
  group?: string;
  required = false;
  defaultValue?: unknown;
  options?: Option[];
  validateText?: ValidateTextFn;
  validateChoice?: ValidateChoiceFn;
  validateChoices?: ValidateChoicesFn;
  when?: Condition;
  actionFn?: ActionFn;
  onError: ErrorAction = ErrorAction.Abort;
  formatFn?: FormatFn;

  constructor(init: {
    key: string;
    kind: StepKind;
    label: string;
    description?: string;
    group?: string;
    required?: boolean;
    defaultValue?: unknown;
    options?: Option[];
    validateText?: ValidateTextFn;
    validateChoice?: ValidateChoiceFn;
    validateChoices?: ValidateChoicesFn;
    when?: Condition;
    actionFn?: ActionFn;
    onError?: ErrorAction;
    formatFn?: FormatFn;
  }) {
    this.key = init.key;
    this.kind = init.kind;
    this.label = init.label;
    this.description = init.description;
    this.group = init.group;
    this.required = init.required ?? false;
    this.defaultValue = init.defaultValue;
    this.options = init.options;
    this.validateText = init.validateText;
    this.validateChoice = init.validateChoice;
    this.validateChoices = init.validateChoices;
    this.when = init.when;
    this.actionFn = init.actionFn;
    this.onError = init.onError ?? ErrorAction.Abort;
    this.formatFn = init.formatFn;
  }

  // --- chainable modifiers (return new Step) ---

  private clone(): Step {
    return new Step({ ...this });
  }

  withRequired(): Step {
    const s = this.clone();
    s.required = true;
    return s;
  }

  withDefault(v: unknown): Step {
    const s = this.clone();
    s.defaultValue = v;
    return s;
  }

  withGroup(g: string): Step {
    const s = this.clone();
    s.group = g;
    return s;
  }

  withDescription(d: string): Step {
    const s = this.clone();
    s.description = d;
    return s;
  }

  withValidateText(fn: ValidateTextFn): Step {
    const s = this.clone();
    s.validateText = fn;
    return s;
  }

  withValidateChoice(fn: ValidateChoiceFn): Step {
    const s = this.clone();
    s.validateChoice = fn;
    return s;
  }

  withValidateChoices(fn: ValidateChoicesFn): Step {
    const s = this.clone();
    s.validateChoices = fn;
    return s;
  }

  withWhen(key: string, pred: (value: unknown) => boolean): Step {
    const s = this.clone();
    s.when = { key, pred };
    return s;
  }

  withOnError(a: ErrorAction): Step {
    const s = this.clone();
    s.onError = a;
    return s;
  }

  withFormat(fn: FormatFn): Step {
    const s = this.clone();
    s.formatFn = fn;
    return s;
  }
}

// --- builders ---

export function textInput(key: string, label: string): Step {
  return new Step({ key, kind: StepKind.TextInput, label });
}

export function select(
  key: string,
  label: string,
  options: Option[],
): Step {
  return new Step({
    key,
    kind: StepKind.Select,
    label,
    options,
  });
}

export function confirm(key: string, label: string): Step {
  return new Step({ key, kind: StepKind.Confirm, label });
}

export function multiSelect(
  key: string,
  label: string,
  options: Option[],
): Step {
  return new Step({
    key,
    kind: StepKind.MultiSelect,
    label,
    options,
  });
}

export function action(
  key: string,
  label: string,
  fn: ActionFn,
): Step {
  return new Step({
    key,
    kind: StepKind.Action,
    label,
    actionFn: fn,
  });
}

export function summary(label: string): Step {
  return new Step({ key: "", kind: StepKind.Summary, label });
}

// --- errors ---

export class ValidationError extends Error {
  stepKey: string;

  constructor(stepKey: string, cause: Error | string) {
    const msg =
      typeof cause === "string" ? cause : cause.message;
    super(
      `validation failed for "${stepKey}": ${msg}`,
    );
    this.name = "ValidationError";
    this.stepKey = stepKey;
  }
}

export class ActionError extends Error {
  stepKey: string;
  cause: Error;
  action: ErrorAction;

  constructor(stepKey: string, cause: Error, act: ErrorAction) {
    super(`action "${stepKey}" failed: ${cause.message}`);
    this.name = "ActionError";
    this.stepKey = stepKey;
    this.cause = cause;
    this.action = act;
  }
}

export class ActionRequest {
  stepKey: string;
  run: ActionFn;

  constructor(stepKey: string, run: ActionFn) {
    this.stepKey = stepKey;
    this.run = run;
  }
}

// --- Wizard ---

export class Wizard {
  private steps: Step[];
  private _current = 0;
  private _results: Record<string, unknown> = {};
  private _done = false;
  private _dryRun = false;
  private _onComplete?: (
    results: Record<string, unknown>,
  ) => void;

  constructor(...steps: Step[]) {
    const seen = new Set<string>();
    for (let i = 0; i < steps.length; i++) {
      const s = steps[i];

      // auto-generate key for Summary
      if (s.kind === StepKind.Summary && s.key === "") {
        s.key = `__summary_${i}`;
      }

      if (s.key === "") {
        throw new Error(
          `step at index ${i}: key must not be empty`,
        );
      }

      if (seen.has(s.key)) {
        throw new Error(`duplicate step key "${s.key}"`);
      }
      seen.add(s.key);

      validateDefault(s);

      if (
        s.kind === StepKind.Action &&
        s.actionFn == null
      ) {
        throw new Error(
          `step "${s.key}": action kind must have actionFn`,
        );
      }

      if (
        (s.kind === StepKind.Select ||
          s.kind === StepKind.MultiSelect) &&
        (!s.options || s.options.length === 0)
      ) {
        throw new Error(
          `step "${s.key}": select/multi_select must have options`,
        );
      }
    }
    this.steps = steps;
  }

  current(): Step | null {
    while (this._current < this.steps.length) {
      const s = this.steps[this._current];
      if (
        s.when != null &&
        !s.when.pred(this._results[s.when.key])
      ) {
        delete this._results[s.key]; // clear stale
        this._current++;
        continue;
      }
      return s;
    }
    this._done = true;
    return null;
  }

  advance(
    value: unknown,
  ): [ActionRequest | null, ValidationError | null] {
    const s = this.current();
    if (s == null) return [null, null];

    switch (s.kind) {
      case StepKind.Action:
        return [
          new ActionRequest(s.key, s.actionFn!),
          null,
        ];

      case StepKind.Summary:
        this.doAdvance();
        return [null, null];

      case StepKind.TextInput:
      case StepKind.Select: {
        if (typeof value !== "string") {
          return [
            null,
            new ValidationError(s.key, "expected string"),
          ];
        }
        if (s.required && value === "") {
          return [
            null,
            new ValidationError(s.key, "required"),
          ];
        }
        if (
          s.kind === StepKind.TextInput &&
          s.validateText
        ) {
          try {
            s.validateText(value);
          } catch (e: unknown) {
            return [
              null,
              new ValidationError(
                s.key,
                e instanceof Error ? e : new Error(String(e)),
              ),
            ];
          }
        }
        if (s.kind === StepKind.Select) {
          if (!isValidOption(s.options!, value)) {
            return [
              null,
              new ValidationError(
                s.key,
                `invalid option "${value}"`,
              ),
            ];
          }
          if (s.validateChoice) {
            try {
              s.validateChoice(value);
            } catch (e: unknown) {
              return [
                null,
                new ValidationError(
                  s.key,
                  e instanceof Error
                    ? e
                    : new Error(String(e)),
                ),
              ];
            }
          }
        }
        this._results[s.key] = value;
        break;
      }

      case StepKind.Confirm: {
        if (typeof value !== "boolean") {
          return [
            null,
            new ValidationError(s.key, "expected bool"),
          ];
        }
        this._results[s.key] = value;
        break;
      }

      case StepKind.MultiSelect: {
        if (
          !Array.isArray(value) ||
          !value.every((v) => typeof v === "string")
        ) {
          return [
            null,
            new ValidationError(
              s.key,
              "expected string[]",
            ),
          ];
        }
        const ss = value as string[];
        if (s.required && ss.length === 0) {
          return [
            null,
            new ValidationError(s.key, "required"),
          ];
        }
        for (const v of ss) {
          if (!isValidOption(s.options!, v)) {
            return [
              null,
              new ValidationError(
                s.key,
                `invalid option "${v}"`,
              ),
            ];
          }
        }
        if (s.validateChoices) {
          try {
            s.validateChoices(ss);
          } catch (e: unknown) {
            return [
              null,
              new ValidationError(
                s.key,
                e instanceof Error
                  ? e
                  : new Error(String(e)),
              ),
            ];
          }
        }
        this._results[s.key] = ss;
        break;
      }
    }

    this.doAdvance();
    return [null, null];
  }

  private doAdvance(): void {
    this._current++;
    if (this.current() == null) {
      this._done = true;
    }
  }

  resolveAction(
    err: Error | null,
  ): ActionError | null {
    const s = this.current();
    if (s == null) return null;

    if (err == null) {
      this.doAdvance();
      return null;
    }

    switch (s.onError) {
      case ErrorAction.Abort:
        return new ActionError(
          s.key,
          err,
          ErrorAction.Abort,
        );
      case ErrorAction.Retry:
        return null;
      case ErrorAction.Skip:
        this.doAdvance();
        return null;
    }
    return new ActionError(s.key, err, s.onError);
  }

  back(): void {
    this._done = false;
    while (this._current > 0) {
      this._current--;
      const s = this.steps[this._current];
      if (
        s.when != null &&
        !s.when.pred(this._results[s.when.key])
      ) {
        continue;
      }
      delete this._results[s.key];
      return;
    }
  }

  results(): Record<string, unknown> {
    return { ...this._results };
  }

  done(): boolean {
    return this._done;
  }

  stepCount(): number {
    let n = 0;
    for (const s of this.steps) {
      if (
        s.when != null &&
        !s.when.pred(this._results[s.when.key])
      ) {
        continue;
      }
      n++;
    }
    return n;
  }

  stepIndex(): number {
    let idx = 0;
    for (
      let i = 0;
      i < this._current && i < this.steps.length;
      i++
    ) {
      const s = this.steps[i];
      if (
        s.when != null &&
        !s.when.pred(this._results[s.when.key])
      ) {
        continue;
      }
      idx++;
    }
    return idx;
  }

  setDryRun(v: boolean): void {
    this._dryRun = v;
  }

  dryRun(): boolean {
    return this._dryRun;
  }

  setOnComplete(
    fn: (results: Record<string, unknown>) => void,
  ): void {
    this._onComplete = fn;
  }

  complete(): void {
    if (this._dryRun || this._onComplete == null) return;
    this._onComplete(this._results);
  }
}

// --- validation helpers ---

function validateDefault(s: Step): void {
  if (s.defaultValue == null) return;

  switch (s.kind) {
    case StepKind.TextInput:
    case StepKind.Select:
      if (typeof s.defaultValue !== "string") {
        throw new Error(
          `step "${s.key}": default must be string for ${s.kind}`,
        );
      }
      break;
    case StepKind.Confirm:
      if (typeof s.defaultValue !== "boolean") {
        throw new Error(
          `step "${s.key}": default must be bool for confirm`,
        );
      }
      break;
    case StepKind.MultiSelect:
      if (
        !Array.isArray(s.defaultValue) ||
        !s.defaultValue.every(
          (v: unknown) => typeof v === "string",
        )
      ) {
        throw new Error(
          `step "${s.key}": default must be string[] for multi_select`,
        );
      }
      break;
  }
}

function isValidOption(opts: Option[], val: string): boolean {
  return opts.some((o) => o.value === val);
}

// --- result accessors ---

export function resultString(
  results: Record<string, unknown>,
  key: string,
): string {
  const v = results[key];
  return typeof v === "string" ? v : "";
}

export function resultBool(
  results: Record<string, unknown>,
  key: string,
): boolean {
  const v = results[key];
  return typeof v === "boolean" ? v : false;
}

export function resultStrings(
  results: Record<string, unknown>,
  key: string,
): string[] | null {
  const v = results[key];
  if (
    Array.isArray(v) &&
    v.every((x) => typeof x === "string")
  ) {
    return v as string[];
  }
  return null;
}

export function resultChoice(
  results: Record<string, unknown>,
  key: string,
): string {
  return resultString(results, key);
}
