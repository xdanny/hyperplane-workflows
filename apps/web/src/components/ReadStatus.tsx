/** Shared read feedback; callers keep cached content visible beside this notice. */
export function ReadStatus({ query, label }: {
  query: { data: unknown; isPending: boolean; isError: boolean; isFetching: boolean; refetch: () => unknown };
  label: string;
}) {
  if (query.isError) return <p role="alert">
    {query.data === undefined ? `Could not load ${label}.` : `Could not refresh ${label}. Showing previously loaded ${label}; they may be out of date.`}{' '}
    <button type="button" className="btn btn--sm" disabled={query.isFetching} onClick={() => { void query.refetch(); }}>Retry {label}</button>
  </p>;
  if (query.isPending) return <p role="status">Loading {label}…</p>;
  return null;
}
