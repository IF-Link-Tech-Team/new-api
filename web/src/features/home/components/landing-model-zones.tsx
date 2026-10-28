/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'

import { getStorefrontModels } from '@/features/storefront/api'

/** The model section is a projection of the live public sales catalog. */
export function LandingModelZones(props: {
  containerRef: RefObject<HTMLDivElement | null>
  content: string
}) {
  const { t } = useTranslation()
  const [targets, setTargets] = useState<HTMLElement[]>([])
  useEffect(() => {
    const shadow = props.containerRef.current?.querySelector(
      '.custom-home-content'
    )?.shadowRoot
    const next = shadow
      ? [...shadow.querySelectorAll<HTMLElement>('[data-storefront-models]')]
      : []
    for (const target of next) target.replaceChildren()
    setTargets(next)
  }, [props.containerRef, props.content])
  const catalog = useQuery({
    queryKey: ['storefront-models'],
    queryFn: getStorefrontModels,
    enabled: targets.length > 0,
    staleTime: 30_000,
    refetchInterval: 60_000,
    retry: false,
  })
  return targets.map((target, index) =>
    createPortal(
      <>
        {catalog.isError && (
          <p role='status'>{t('Models are temporarily unavailable')}</p>
        )}
        {catalog.isPending && <p role='status'>{t('Loading...')}</p>}
        {catalog.isSuccess && catalog.data.length === 0 && (
          <p role='status'>{t('No models available')}</p>
        )}
        {!catalog.isError &&
          catalog.data?.map((model) => (
            <a
              className='model-card'
              key={model.model_name}
              href={`/pricing?${new URLSearchParams({ search: model.model_name })}`}
            >
              <span className='model-mark' aria-hidden='true'>
                AI
              </span>
              <span>
                <strong>{model.model_name}</strong>
                <small>
                  {model.description || t('View current prices and access')}
                </small>
              </span>
            </a>
          ))}
      </>,
      target,
      `live-models-${index}`
    )
  )
}
