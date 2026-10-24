import { Component, type ReactNode } from "react";
import { Button } from "@/components/ui/button";

type Props = {
  children: ReactNode;
  message: string;
  retryLabel: string;
};

export default class SectionBoundary extends Component<Props, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <div role="alert" className="space-y-3 rounded-xl border bg-card p-5">
        <p>{this.props.message}</p>
        {/* shortcut: retry reloads the page; add chunk-only recovery if offline use is required. */}
        <Button variant="outline" onClick={() => location.reload()}>
          {this.props.retryLabel}
        </Button>
      </div>
    );
  }
}
