import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import { modelOptions, type ModelOption } from './options'

// useModelOptions is the model dropdown's options, the same in New loop and
// on the loop page. Until the list loads, and on a hub without the route, it
// is the four families with their descriptions, so the form is never empty.
// The stream's models frame keeps it fresh as resolutions land (#332).
export function useModelOptions(): ModelOption[] {
  const { data } = useQuery({ queryKey: ['models'], queryFn: api.models })
  return modelOptions(data)
}
