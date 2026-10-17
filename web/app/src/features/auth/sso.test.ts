// SPDX-License-Identifier: Apache-2.0

import { afterEach, describe, expect, it } from "vitest";
import { rememberReturnHash, ssoFailureMessages, ssoStartURL, takeStashedHash } from "./sso";

afterEach(() => sessionStorage.clear());

describe("ssoStartURL", () => {
  it("sends only the path to the server", () => {
    expect(ssoStartURL("/projects/prj_1?tab=x#secret")).toBe("/api/v1/sso/start?return=%2Fprojects%2Fprj_1%3Ftab%3Dx");
    expect(ssoStartURL("#only-a-hash")).toBe("/api/v1/sso/start?return=%2F");
  });
});

describe("rememberReturnHash", () => {
  it("hands the hash back once, for the same path", () => {
    rememberReturnHash("/invite#token-123");
    expect(takeStashedHash("/invite")).toBe("token-123");
    expect(takeStashedHash("/invite")).toBeUndefined();
  });

  it("doesn't hand a hash to another page", () => {
    rememberReturnHash("/invite#token-123");
    expect(takeStashedHash("/workspaces")).toBeUndefined();
    expect(takeStashedHash("/invite")).toBeUndefined(); // used up either way
  });

  it("remembers nothing without a hash", () => {
    rememberReturnHash("/workspaces");
    expect(sessionStorage.length).toBe(0);
  });
});

describe("ssoFailureMessages", () => {
  it("explains every reason the server sends", () => {
    for (const reason of ["expired", "failed", "denied", "unavailable", "email-unverified", "not-allowed", "account-exists", "rate-limited"]) {
      expect(ssoFailureMessages[reason], reason).toMatch(/\.$/);
    }
  });
});
