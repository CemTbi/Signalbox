// SPDX-License-Identifier: Apache-2.0

import { Background, BackgroundVariant, Controls, type Edge, type Node, ReactFlow } from "@xyflow/react";
import "@xyflow/react/dist/style.css";

const nodes: Node[] = [];
const edges: Edge[] = [];

/**
 * A project's canvas. Services become nodes on it, and the lines between
 * them come from declared dependencies and from traces. Until a project has
 * services, the canvas explains what will appear.
 */
export default function Canvas() {
  return (
    <div className="canvas">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        fitView
        proOptions={{ hideAttribution: true }}
        aria-label="Service canvas"
        colorMode="system"
      >
        <Background variant={BackgroundVariant.Dots} gap={24} size={1.5} />
        <Controls showInteractive={false} />
      </ReactFlow>
      <div className="canvas-empty">
        <p className="empty-title">No services on this canvas yet</p>
        <p>
          Each service of the project will be a node here, showing its spec version, health and latency. Lines will
          show which services call each other.
        </p>
      </div>
    </div>
  );
}
