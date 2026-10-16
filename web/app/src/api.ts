// SPDX-License-Identifier: Apache-2.0

import { createApiClient } from "@signalbox/api-client";

/** The app's shared API client. Components accept another one in tests. */
export const api = createApiClient();
