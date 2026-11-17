import { Component } from 'react';
import type { ReactNode, ErrorInfo } from 'react';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { NavRail } from './components/NavRail';
import { Home } from './views/Home';
import { Runs } from './views/Runs';
import { Workflows } from './views/Workflows';
import { WorkflowHistory } from './views/WorkflowHistory';
import { Inbox } from './views/Inbox';
import { Knowledge } from './views/Knowledge';
import { Board } from './views/Board';
import { Console } from './views/Console';
import { TokenGate } from './components/TokenGate';

interface ErrorBoundaryState {
  error: Error | null;
}

class ErrorBoundary extends Component<{ children: ReactNode }, ErrorBoundaryState> {
  constructor(props: { children: ReactNode }) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('[ErrorBoundary]', error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="errbox">
          <div className="h2">Render error</div>
          <pre>{this.state.error.message}{'\n\n'}{this.state.error.stack}</pre>
        </div>
      );
    }
    return this.props.children;
  }
}

export function App() {
  return (
    <ErrorBoundary>
      <TokenGate>
        <BrowserRouter>
          <div className="layout">
            <NavRail />
            <main className="main-content">
              <Routes>
                <Route path="/"          element={<ErrorBoundary><Home /></ErrorBoundary>}      />
                <Route path="/console"   element={<ErrorBoundary><Console /></ErrorBoundary>}   />
                <Route path="/runs"      element={<ErrorBoundary><Runs /></ErrorBoundary>}      />
                {/* A run detail page is a URL. On-call people paste run links
                    into incident channels, and the id in the path is also what
                    lets a reload put the reader back where they were. */}
                <Route path="/runs/:runId" element={<ErrorBoundary><Runs /></ErrorBoundary>}   />
                {/* Projects is the rail inside Runs, but it is also a place a
                    person points someone else at. `/projects` opens the panel;
                    `/projects/<tag>` opens that project (P-13). */}
                <Route path="/projects"    element={<ErrorBoundary><Runs /></ErrorBoundary>}  />
                <Route path="/projects/:projectId" element={<ErrorBoundary><Runs /></ErrorBoundary>} />
                {/* Ordinary goals launch directly as checked SDK tasks. */}
                <Route path="/workflows" element={<ErrorBoundary><Workflows /></ErrorBoundary>} />
                {/* A saved workflow's own history is a place people link to —
                    "here is every run of the Monday brief" (F-C). */}
                <Route path="/workflows/:workflowId" element={<ErrorBoundary><WorkflowHistory /></ErrorBoundary>} />
                <Route path="/inbox"     element={<ErrorBoundary><Inbox /></ErrorBoundary>}     />
                <Route path="/knowledge" element={<ErrorBoundary><Knowledge /></ErrorBoundary>} />
                <Route path="/board"     element={<ErrorBoundary><Board /></ErrorBoundary>}     />
                <Route path="*"          element={<Navigate to="/" replace />} />
              </Routes>
            </main>
          </div>
        </BrowserRouter>
      </TokenGate>
    </ErrorBoundary>
  );
}
