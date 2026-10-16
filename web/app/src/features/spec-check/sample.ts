// SPDX-License-Identifier: Apache-2.0

/**
 * The document the page opens with. It has one real error, a $ref to a
 * schema that doesn't exist, and a few quality warnings, so the first check
 * shows what Signalbox reports.
 */
export const sampleSpec = `openapi: 3.1.1
info:
  title: Orders API
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /orders/{orderId}:
    get:
      summary: Get an order
      tags: [orders]
      parameters:
        - name: orderId
          in: path
          required: true
          schema:
            type: string
            format: uuid
      responses:
        '200':
          description: The order.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Order'
        '404':
          description: No order has this ID.
tags:
  - name: orders
    description: Orders placed in the shop.
`;
