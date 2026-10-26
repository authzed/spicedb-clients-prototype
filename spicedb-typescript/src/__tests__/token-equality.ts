import { expect } from "vitest";
import { ZedToken } from "../token.js";
expect.addEqualityTesters([(a: unknown, b: unknown) => {
    if (a instanceof ZedToken || b instanceof ZedToken)
        return a instanceof ZedToken && b instanceof ZedToken && a.toString() === b.toString();
    return undefined;
}]);
