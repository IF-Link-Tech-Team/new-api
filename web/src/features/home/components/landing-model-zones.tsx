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

import { getPricing } from '@/features/pricing/api'
import {
  getDynamicPricingSummary,
  getDynamicDisplayGroupRatio,
  isDynamicPricingModel,
} from '@/features/pricing/lib/dynamic-price'
import { getAvailableGroups } from '@/features/pricing/lib/model-helpers'
import { formatPrice, formatRequestPrice } from '@/features/pricing/lib/price'
import type { PricingModel } from '@/features/pricing/types'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import {
  readLandingModelZones,
  type ZoneTarget,
} from '../lib/landing-model-zones'

function LandingModelPrice(props: { model: PricingModel; group: string }) {
  const { t } = useTranslation()
  // Existing formatters read this store; subscribe to late configuration updates.
  useSystemConfigStore((state) => state.config.currency)
  const model = props.model
  if (isDynamicPricingModel(model)) {
    const summary = getDynamicPricingSummary(model, {
      tokenUnit: 'M',
      groupRatioMultiplier: getDynamicDisplayGroupRatio(model, props.group),
    })
    if (
      !summary ||
      summary.isSpecialExpression ||
      summary.hasRequestRules ||
      summary.tierCount !== 1
    ) {
      return <p className='zone-model-status'>{t('Dynamic Pricing')}</p>
    }
    return (
      <dl className='zone-model-prices'>
        {summary.entries.map((entry) => (
          <div key={entry.key}>
            <dt>{t(entry.shortLabel)}</dt>
            <dd>{entry.formatted}</dd>
          </div>
        ))}
      </dl>
    )
  }
  if (model.quota_type === 1) {
    return (
      <p className='zone-model-status'>
        {formatRequestPrice(model, false, 1, 1, props.group)} / {t('request')}
      </p>
    )
  }
  const types = ['input', 'output'] as const
  return (
    <dl className='zone-model-prices'>
      {types.map((type) => (
        <div key={type}>
          <dt>{type === 'input' ? t('Input') : t('Output')}</dt>
          <dd>{formatPrice(model, type, 'M', false, 1, 1, props.group)}</dd>
        </div>
      ))}
      {model.cache_ratio != null && (
        <div>
          <dt>{t('Cached')}</dt>
          <dd>{formatPrice(model, 'cache', 'M', false, 1, 1, props.group)}</dd>
        </div>
      )}
    </dl>
  )
}

export function LandingModelZones(props: {
  containerRef: RefObject<HTMLDivElement | null>
  content: string
}) {
  const { t } = useTranslation()
  const { auth } = useAuthStore()
  const authenticated = Boolean(auth.user && auth.accessToken)
  const [targets, setTargets] = useState<ZoneTarget[]>([])

  useEffect(() => {
    const shadow = props.containerRef.current?.querySelector(
      '.custom-home-content'
    )?.shadowRoot
    const next = shadow ? readLandingModelZones(shadow) : []
    for (const target of next) target.element.replaceChildren()
    setTargets(next)
  }, [props.containerRef, props.content])

  const pricing = useQuery({
    queryKey: [
      'pricing',
      'landing-model-zones',
      auth.user?.id,
      auth.session?.sid,
    ],
    queryFn: async () => {
      const response = await getPricing()
      if (!response.success) throw new Error('Pricing unavailable')
      return response
    },
    enabled: authenticated && targets.length > 0,
    retry: false,
    staleTime: 60_000,
  })

  return targets.map((target) =>
    createPortal(
      <>
        {target.models.map((item) => {
          const model =
            authenticated && !pricing.isError
              ? pricing.data?.data.find(
                  (entry) => entry.model_name === item.model_id
                )
              : undefined
          const groups = model
            ? getAvailableGroups(model, pricing.data?.usable_group || {})
            : []
          if (
            authenticated &&
            pricing.isSuccess &&
            (!model || groups.length === 0)
          ) {
            return null
          }
          const userGroup = auth.user?.group
          const group =
            userGroup && groups.includes(userGroup) ? userGroup : groups[0]
          const pricingSearch = new URLSearchParams({ search: item.model_id })
          if (group) pricingSearch.set('group', group)
          const pricingHref = `/pricing?${pricingSearch}`
          let status = t('Sign in to view current prices')
          if (authenticated) {
            status = pricing.isError
              ? t('Current prices unavailable')
              : t('Loading...')
          }
          return (
            <article className='zone-model-card' key={item.model_id}>
              <p className='zone-model-scenario'>{item.scenario}</p>
              <h4>{item.name}</h4>
              <code className='zone-model-id'>{item.model_id}</code>
              <p className='zone-model-description'>{item.description}</p>
              {model && group ? (
                <>
                  <LandingModelPrice
                    model={{ ...model, group_ratio: pricing.data?.group_ratio }}
                    group={group}
                  />
                  <p className='zone-model-status'>
                    {t('Group')}: {group} · {t('Per 1M tokens')}
                  </p>
                </>
              ) : (
                <p className='zone-model-status' role='status'>
                  {status}
                </p>
              )}
              <a className='zone-model-link' href={pricingHref}>
                {t('View current prices and access')} →
              </a>
            </article>
          )
        })}
        {authenticated &&
          pricing.isSuccess &&
          !target.models.some((item) => {
            const model = pricing.data.data.find(
              (entry) => entry.model_name === item.model_id
            )
            return (
              model &&
              getAvailableGroups(model, pricing.data.usable_group).length > 0
            )
          }) && (
            <p className='zone-model-empty' role='status'>
              {t('No models available for this account')}
            </p>
          )}
      </>,
      target.element,
      target.element.closest('section')?.id
    )
  )
}
