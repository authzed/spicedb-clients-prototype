import { describe, it, expect } from "vitest";
import { full, minLatency, atLeast, atLeastOrFull, atLeastOrMinLatency, snapshot } from "../consistency.js";

describe("consistency strategies", () => {
  it("full() creates fully consistent requirement", () => {
    const c = full();
    expect(c.requirement.case).toBe("fullyConsistent");
    expect(c.requirement.value).toBe(true);
  });

  it("minLatency() creates minimize latency requirement", () => {
    const c = minLatency();
    expect(c.requirement.case).toBe("minimizeLatency");
    expect(c.requirement.value).toBe(true);
  });

  it("atLeast() creates at-least-as-fresh requirement", () => {
    const c = atLeast("some-token");
    expect(c.requirement.case).toBe("atLeastAsFresh");
    if (c.requirement.case === "atLeastAsFresh") {
      expect(c.requirement.value.token).toBe("some-token");
    }
  });

  it("atLeastOrFull() with revision returns atLeastAsFresh", () => {
    const c = atLeastOrFull("some-token");
    expect(c.requirement.case).toBe("atLeastAsFresh");
    if (c.requirement.case === "atLeastAsFresh") {
      expect(c.requirement.value.token).toBe("some-token");
    }
  });

  it("atLeastOrFull() with empty string returns fullyConsistent", () => {
    const c = atLeastOrFull("");
    expect(c.requirement.case).toBe("fullyConsistent");
    expect(c.requirement.value).toBe(true);
  });

  it("atLeastOrMinLatency() with revision returns atLeastAsFresh", () => {
    const c = atLeastOrMinLatency("some-token");
    expect(c.requirement.case).toBe("atLeastAsFresh");
    if (c.requirement.case === "atLeastAsFresh") {
      expect(c.requirement.value.token).toBe("some-token");
    }
  });

  it("atLeastOrMinLatency() with empty string returns minimizeLatency", () => {
    const c = atLeastOrMinLatency("");
    expect(c.requirement.case).toBe("minimizeLatency");
    expect(c.requirement.value).toBe(true);
  });

  it("snapshot() creates at-exact-snapshot requirement", () => {
    const c = snapshot("some-token");
    expect(c.requirement.case).toBe("atExactSnapshot");
    if (c.requirement.case === "atExactSnapshot") {
      expect(c.requirement.value.token).toBe("some-token");
    }
  });
});
