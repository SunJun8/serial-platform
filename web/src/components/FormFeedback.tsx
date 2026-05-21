import type { RequestState } from '../types';

export function FormFeedback({ state }: { state: RequestState }) {
  if (state.error) {
    return <div className="inline-error">{state.error}</div>;
  }
  if (state.message) {
    return <div className="inline-success">{state.message}</div>;
  }
  return null;
}
