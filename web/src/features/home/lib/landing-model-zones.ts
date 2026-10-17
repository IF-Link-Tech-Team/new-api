/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
interface ZoneModel {
  model_id: string
  name: string
  scenario: string
  description: string
}

export interface ZoneTarget {
  element: HTMLElement
  models: ZoneModel[]
}

/** Read only the presentation manifest embedded by the Landing renderer. */
export function readLandingModelZones(root: ParentNode): ZoneTarget[] {
  const targets: ZoneTarget[] = []
  for (const element of root.querySelectorAll<HTMLElement>(
    '[data-landing-models]'
  )) {
    try {
      const models: unknown = JSON.parse(element.dataset.landingModels || '')
      if (
        !Array.isArray(models) ||
        models.length === 0 ||
        models.length > 8 ||
        !models.every((model: unknown) => {
          if (typeof model !== 'object' || model === null) return false
          return ['model_id', 'name', 'scenario', 'description'].every(
            (field) =>
              field in model &&
              typeof model[field as keyof typeof model] === 'string'
          )
        })
      ) {
        continue
      }
      targets.push({ element, models: models as ZoneModel[] })
    } catch {
      // Invalid or older custom home content retains its ordinary pricing links.
    }
  }
  return targets
}
