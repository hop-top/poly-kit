import { describe, it, expect } from "vitest";
import {
  StepKind,
  ErrorAction,
  Wizard,
  textInput,
  select,
  confirm,
  multiSelect,
  action,
  summary,
  ValidationError,
  ActionError,
  ActionRequest,
  resultString,
  resultBool,
  resultStrings,
  resultChoice,
  type Step,
} from "./wizard";

// --- construction ---

describe("Wizard construction", () => {
  it("accepts valid steps", () => {
    const w = new Wizard(
      textInput("name", "Name"),
      confirm("ok", "OK?"),
    );
    expect(w).toBeTruthy();
  });

  it("rejects duplicate keys", () => {
    expect(
      () => new Wizard(textInput("x", "A"), textInput("x", "B")),
    ).toThrow(/duplicate/i);
  });

  it("rejects empty key", () => {
    expect(
      () =>
        new Wizard({
          key: "",
          kind: StepKind.TextInput,
          label: "Name",
        } as Step),
    ).toThrow(/key must not be empty/i);
  });

  it("rejects bad default type for confirm", () => {
    expect(
      () => new Wizard(confirm("ok", "OK?").withDefault("nope")),
    ).toThrow(/bool/i);
  });

  it("rejects action without fn", () => {
    expect(
      () =>
        new Wizard({
          key: "act",
          kind: StepKind.Action,
          label: "do",
        } as Step),
    ).toThrow(/actionFn/i);
  });

  it("rejects select without options", () => {
    expect(
      () =>
        new Wizard({
          key: "sel",
          kind: StepKind.Select,
          label: "pick",
          options: [],
        } as Step),
    ).toThrow(/options/i);
  });

  it("auto-generates summary key", () => {
    const w = new Wizard(
      textInput("a", "A"),
      summary("Review"),
    );
    expect(w.current()!.key).toBe("a");
  });
});

// --- advance ---

describe("Advance", () => {
  it("text input stores string", () => {
    const w = new Wizard(textInput("name", "Name"));
    const [res, err] = w.advance("Alice");
    expect(err).toBeNull();
    expect(res).toBeNull();
    expect(resultString(w.results(), "name")).toBe("Alice");
    expect(w.done()).toBe(true);
  });

  it("select stores valid option", () => {
    const opts = [
      { value: "a", label: "A" },
      { value: "b", label: "B" },
    ];
    const w = new Wizard(select("color", "Color", opts));
    w.advance("a");
    expect(resultChoice(w.results(), "color")).toBe("a");
  });

  it("select rejects invalid option", () => {
    const opts = [
      { value: "a", label: "A" },
      { value: "b", label: "B" },
    ];
    const w = new Wizard(select("color", "Color", opts));
    const [, err] = w.advance("z");
    expect(err).toBeInstanceOf(ValidationError);
    expect(err!.message).toMatch(/invalid option/);
  });

  it("confirm stores bool", () => {
    const w = new Wizard(confirm("ok", "Sure?"));
    w.advance(true);
    expect(resultBool(w.results(), "ok")).toBe(true);
  });

  it("confirm rejects non-bool", () => {
    const w = new Wizard(confirm("ok", "Sure?"));
    const [, err] = w.advance("yes");
    expect(err).toBeInstanceOf(ValidationError);
  });

  it("multi_select stores string array", () => {
    const opts = [
      { value: "x", label: "X" },
      { value: "y", label: "Y" },
    ];
    const w = new Wizard(multiSelect("tags", "Tags", opts));
    w.advance(["x", "y"]);
    expect(resultStrings(w.results(), "tags")).toEqual(["x", "y"]);
  });

  it("multi_select rejects invalid option", () => {
    const opts = [
      { value: "x", label: "X" },
      { value: "y", label: "Y" },
    ];
    const w = new Wizard(multiSelect("tags", "Tags", opts));
    const [, err] = w.advance(["x", "nope"]);
    expect(err).toBeInstanceOf(ValidationError);
    expect(err!.message).toMatch(/invalid option.*nope/);
  });

  it("action returns ActionRequest", () => {
    let _called = false;
    const fn = async () => {
      _called = true;
    };
    const w = new Wizard(action("act", "Go", fn));
    const [res, err] = w.advance(null);
    expect(err).toBeNull();
    expect(res).toBeInstanceOf(ActionRequest);
    expect((res as ActionRequest).stepKey).toBe("act");
  });

  it("summary advances without value", () => {
    const w = new Wizard(
      textInput("a", "A"),
      summary("Review"),
    );
    w.advance("val");
    expect(w.current()!.kind).toBe(StepKind.Summary);
    w.advance(null);
    expect(w.done()).toBe(true);
  });

  it("validates required text", () => {
    const w = new Wizard(textInput("name", "Name").withRequired());
    const [, err] = w.advance("");
    expect(err).toBeInstanceOf(ValidationError);
    expect(err!.message).toMatch(/required/);
  });

  it("validates required multi_select", () => {
    const opts = [{ value: "x", label: "X" }];
    const w = new Wizard(
      multiSelect("tags", "Tags", opts).withRequired(),
    );
    const [, err] = w.advance([]);
    expect(err).toBeInstanceOf(ValidationError);
    expect(err!.message).toMatch(/required/);
  });

  it("runs custom text validator", () => {
    const w = new Wizard(
      textInput("email", "Email").withValidateText((s) => {
        if (s === "bad") throw new Error("invalid email");
      }),
    );
    const [, err] = w.advance("bad");
    expect(err).toBeInstanceOf(ValidationError);
    expect(err!.message).toMatch(/invalid email/);
  });
});

// --- resolveAction ---

describe("ResolveAction", () => {
  it("advances on success", () => {
    const w = new Wizard(
      action("a", "A", async () => {}),
      textInput("b", "B"),
    );
    w.advance(null); // get ActionRequest
    const err = w.resolveAction(null);
    expect(err).toBeNull();
    expect(w.current()!.key).toBe("b");
  });

  it("returns ActionError on abort", () => {
    const w = new Wizard(
      action("a", "A", async () => {}).withOnError(
        ErrorAction.Abort,
      ),
    );
    w.advance(null);
    const err = w.resolveAction(new Error("boom"));
    expect(err).toBeInstanceOf(ActionError);
    expect((err as ActionError).action).toBe(ErrorAction.Abort);
  });

  it("stays on step for retry", () => {
    const w = new Wizard(
      action("a", "A", async () => {}).withOnError(
        ErrorAction.Retry,
      ),
    );
    w.advance(null);
    const err = w.resolveAction(new Error("transient"));
    expect(err).toBeNull();
    expect(w.current()!.key).toBe("a");
  });

  it("skips on skip policy", () => {
    const w = new Wizard(
      action("a", "A", async () => {}).withOnError(
        ErrorAction.Skip,
      ),
      textInput("b", "B"),
    );
    w.advance(null);
    const err = w.resolveAction(new Error("meh"));
    expect(err).toBeNull();
    expect(w.current()!.key).toBe("b");
  });
});

// --- back ---

describe("Back", () => {
  it("goes to previous step and clears result", () => {
    const w = new Wizard(textInput("a", "A"), textInput("b", "B"));
    w.advance("hello");
    expect(w.current()!.key).toBe("b");
    w.back();
    expect(w.current()!.key).toBe("a");
    expect(resultString(w.results(), "a")).toBe("");
  });

  it("no-op at start", () => {
    const w = new Wizard(textInput("a", "A"));
    w.back();
    expect(w.current()!.key).toBe("a");
  });

  it("skips hidden steps during back", () => {
    const w = new Wizard(
      confirm("show", "Show?"),
      textInput("hidden", "Hidden").withWhen("show", (v) =>
        Boolean(v),
      ),
      textInput("last", "Last"),
    );
    w.advance(false);
    expect(w.current()!.key).toBe("last");
    w.back();
    expect(w.current()!.key).toBe("show");
  });
});

// --- conditional ---

describe("Conditional steps", () => {
  it("shows step when condition true", () => {
    const w = new Wizard(
      confirm("advanced", "Advanced?"),
      textInput("extra", "Extra").withWhen("advanced", (v) =>
        Boolean(v),
      ),
    );
    w.advance(true);
    expect(w.current()!.key).toBe("extra");
  });

  it("skips step when condition false", () => {
    const w = new Wizard(
      confirm("advanced", "Advanced?"),
      textInput("extra", "Extra").withWhen("advanced", (v) =>
        Boolean(v),
      ),
    );
    w.advance(false);
    expect(w.done()).toBe(true);
  });

  it("clears stale results on re-evaluation", () => {
    const w = new Wizard(
      confirm("show", "Show?"),
      textInput("extra", "Extra").withWhen("show", (v) =>
        Boolean(v),
      ),
      textInput("last", "Last"),
    );
    // show=true, fill extra
    w.advance(true);
    w.advance("filled");
    expect(w.results()["extra"]).toBe("filled");

    // go back, change show to false
    w.back(); // back to extra
    w.back(); // back to show
    w.advance(false);

    expect(w.current()!.key).toBe("last");
    expect(w.results()["extra"]).toBeUndefined();
  });
});

// --- done ---

describe("Done", () => {
  it("false initially, true after all steps", () => {
    const w = new Wizard(textInput("a", "A"));
    expect(w.done()).toBe(false);
    w.advance("val");
    expect(w.done()).toBe(true);
  });
});

// --- stepCount / stepIndex ---

describe("StepCount and StepIndex", () => {
  it("excludes hidden steps", () => {
    const w = new Wizard(
      confirm("show", "Show?"),
      textInput("hidden", "Hidden").withWhen("show", (v) =>
        Boolean(v),
      ),
      textInput("visible", "Visible"),
    );
    // before answering, show default nil → pred false
    expect(w.stepCount()).toBe(2);
    w.advance(true);
    expect(w.stepCount()).toBe(3);
  });

  it("stepIndex tracks visible position", () => {
    const w = new Wizard(
      textInput("a", "A"),
      textInput("b", "B"),
      textInput("c", "C"),
    );
    expect(w.stepIndex()).toBe(0);
    w.advance("x");
    expect(w.stepIndex()).toBe(1);
    w.advance("y");
    expect(w.stepIndex()).toBe(2);
  });
});

// --- dryRun / complete ---

describe("Complete and DryRun", () => {
  it("calls onComplete", () => {
    const w = new Wizard(textInput("a", "A"));
    let got: Record<string, unknown> | null = null;
    w.setOnComplete((r) => {
      got = r;
    });
    w.advance("done");
    w.complete();
    expect(got!["a"]).toBe("done");
  });

  it("skips onComplete in dryRun", () => {
    const w = new Wizard(textInput("a", "A"));
    let called = false;
    w.setOnComplete(() => {
      called = true;
    });
    w.setDryRun(true);
    expect(w.dryRun()).toBe(true);
    w.advance("val");
    w.complete();
    expect(called).toBe(false);
  });
});

// --- result accessors ---

describe("Result accessors", () => {
  const results: Record<string, unknown> = {
    name: "Alice",
    ok: true,
    tags: ["a", "b"],
    color: "red",
    bad: 42,
  };

  it("string", () => {
    expect(resultString(results, "name")).toBe("Alice");
    expect(resultString(results, "missing")).toBe("");
    expect(resultString(results, "bad")).toBe("");
  });

  it("bool", () => {
    expect(resultBool(results, "ok")).toBe(true);
    expect(resultBool(results, "missing")).toBe(false);
    expect(resultBool(results, "bad")).toBe(false);
  });

  it("strings", () => {
    expect(resultStrings(results, "tags")).toEqual(["a", "b"]);
    expect(resultStrings(results, "missing")).toBeNull();
    expect(resultStrings(results, "bad")).toBeNull();
  });

  it("choice (alias for string)", () => {
    expect(resultChoice(results, "color")).toBe("red");
  });
});
