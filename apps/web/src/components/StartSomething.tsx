import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ProjectPicker, useHasProjects, NO_PROJECTS_HINT } from './ProjectPicker';

// Capture intent here; the workflow editor prepares steps before starting.
export function StartSomething({ defaultProject = '' }: { defaultProject?: string }) {
  const navigate = useNavigate();
  const [brief, setBrief] = useState('');
  const [project, setProject] = useState(defaultProject);
  const hasProjects = useHasProjects();

  const start = () => {
    if (brief.trim()) void navigate('/workflows', { state: { brief: brief.trim(), project } });
  };

  return (
    <section className="panel start-card" aria-label="Start something">
      <div className="start-card-head">
        <span className="eyebrow">Start something</span>
        <span className="start-card-hint mono faint">⌘↵ to continue</span>
      </div>

      <textarea
        className="input start-card-input"
        rows={3}
        value={brief}
        placeholder="Describe what you want — research, a plan, a decision, an app…"
        aria-label="Describe what you want"
        aria-describedby="start-card-guidance"
        onChange={(e) => setBrief(e.target.value)}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
            e.preventDefault();
            start();
          }
        }}
      />

      {/* What pressing the button actually does, what it costs, and what a good
          goal contains — none of which the front door used to say (Q-26, P-10). */}
      <p className="composer-guidance" id="start-card-guidance">Build your steps by dragging step types, describing them, or starting from a template on the next screen. No work starts yet.</p>

      <div className="start-card-foot">
        <ProjectPicker value={project} onChange={setProject} />
        <button className="btn btn--primary" disabled={!brief.trim()} onClick={start}>
          Set up workflow
        </button>
      </div>

      {!hasProjects && (
        <p className="start-card-note faint">{NO_PROJECTS_HINT}</p>
      )}
    </section>
  );
}
