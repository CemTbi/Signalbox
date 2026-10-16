// SPDX-License-Identifier: Apache-2.0

// Vite empties the output directory on every build. Put the placeholder back
// so internal/webui/dist always exists for go:embed, even in a fresh clone.
import { writeFileSync } from "node:fs";

writeFileSync(new URL("../../../internal/webui/dist/.gitkeep", import.meta.url), "");
