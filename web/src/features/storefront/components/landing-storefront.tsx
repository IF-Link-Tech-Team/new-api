/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'

import { buildCheckoutPath } from '@/features/checkout/lib/checkout-path'
import { cnyToComputePoints, formatComputePoints } from '@/lib/compute-points'

import { getStorefrontCatalog } from '../api'
import {
  formatStorefrontPeriod,
  formatStorefrontPrice,
  formatStorefrontValidity,
  isStorefrontItemPurchasable,
  normalizeStorefrontCatalog,
  storefrontItemsForMount,
} from '../lib/storefront-catalog'
import { readStorefrontMounts } from '../lib/storefront-mounts'
import type {
  StorefrontCatalogItem,
  StorefrontMount,
} from '../types'

function StorefrontBadges(props: { item: StorefrontCatalogItem }) {
  const badges = props.item.display_metadata.badges ?? []
  if (badges.length === 0) return null
  return (
    <div className='card-badges'>
      {badges.map((badge) => (
        <span key={badge.label} className={`badge badge--${badge.tone}`}>
          {badge.label}
        </span>
      ))}
    </div>
  )
}

function StorefrontAction(props: {
  item: StorefrontCatalogItem
  activeClassName: string
  idleClassName: string
  label?: string
}) {
  const { t } = useTranslation()
  const item = props.item
  const label = props.label ?? item.display_metadata.button_label
  if (isStorefrontItemPurchasable(item)) {
    return (
      <a
        className={props.activeClassName}
        href={buildCheckoutPath(item.product_id)}
        data-payment-product-id={item.product_id}
      >
        {label ?? t('Buy Now')}
      </a>
    )
  }
  return (
    <button className={props.idleClassName} type='button' disabled>
      {label ?? t('Not on sale yet')}
    </button>
  )
}

function StorefrontPlanCard(props: { item: StorefrontCatalogItem }) {
  const item = props.item
  const metadata = item.display_metadata
  const benefits = metadata.benefits ?? []
  return (
    <article
      className={
        metadata.recommended ? 'plan-card plan-card--recommended' : 'plan-card'
      }
    >
      <StorefrontBadges item={item} />
      <h3>{item.title}</h3>
      <p className='card-description'>{item.subtitle}</p>
      <p className='plan-price'>
        <strong>{formatStorefrontPrice(item.price_amount, item.currency)}</strong>
        <span>{formatStorefrontPeriod(item.duration_unit, item.duration_value)}</span>
      </p>
      {benefits.length > 0 && (
        <ul className='benefit-list'>
          {benefits.map((benefit) => (
            <li key={benefit}>{benefit}</li>
          ))}
        </ul>
      )}
      <StorefrontAction
        item={item}
        activeClassName='purchase-button purchase-button--active'
        idleClassName='purchase-button'
      />
    </article>
  )
}

function StorefrontPackCard(props: { item: StorefrontCatalogItem }) {
  const item = props.item
  const metadata = item.display_metadata
  const points =
    item.currency.toUpperCase() === 'CNY'
      ? formatComputePoints(cnyToComputePoints(item.price_amount))
      : ''
  const validity = formatStorefrontValidity(item.duration_unit, item.duration_value)
  const models = metadata.models_display ?? item.model_scope.split(',').join(' / ')
  return (
    <article
      className={metadata.featured ? 'pack-card pack-card--featured' : 'pack-card'}
    >
      <StorefrontBadges item={item} />
      <h3>{item.title}</h3>
      <p className='card-description'>{item.subtitle}</p>
      <dl className='pack-facts'>
        {points && (
          <div>
            <dt>额度</dt>
            <dd>{points}</dd>
          </div>
        )}
        {validity && (
          <div>
            <dt>有效期</dt>
            <dd>{validity}</dd>
          </div>
        )}
        {models && (
          <div>
            <dt>适用模型</dt>
            <dd>{models}</dd>
          </div>
        )}
      </dl>
      <div className='pack-price'>
        <strong>{formatStorefrontPrice(item.price_amount, item.currency)}</strong>
        {metadata.reference_price && <s>{metadata.reference_price}</s>}
      </div>
      <StorefrontAction
        item={item}
        activeClassName='purchase-button purchase-button--active'
        idleClassName='purchase-button'
      />
    </article>
  )
}

function StorefrontZonePackCard(props: { item: StorefrontCatalogItem }) {
  const item = props.item
  const metadata = item.display_metadata
  const points =
    item.currency.toUpperCase() === 'CNY'
      ? formatComputePoints(cnyToComputePoints(item.price_amount))
      : ''
  const validity = formatStorefrontValidity(item.duration_unit, item.duration_value)
  const tier = item.title.trim().split(/\s+/).pop() ?? item.title
  const price = `¥${item.price_amount.toFixed(2)}`
  return (
    <article className='bigfish-pack-card' data-bigfish-pack={item.product_id}>
      {metadata.image && (
        <div className='bigfish-pack-art'>
          <img
            src={metadata.image}
            alt={metadata.image_alt ?? item.title}
            width='460'
            height='384'
            loading='lazy'
            decoding='async'
          />
        </div>
      )}
      <h4>{item.title}</h4>
      <p className='bigfish-pack-price'>
        {price}
        <span> / 一次性</span>
      </p>
      {points && (
        <p className='bigfish-pack-quota'>
          {points.split(' ')[0]} <span>算力点</span>
        </p>
      )}
      <p className='bigfish-pack-meta'>
        {validity ? `${validity}有效 · 独立专属 Key` : '独立专属 Key'}
      </p>
      <StorefrontAction
        item={item}
        activeClassName='iflink-button starter-purchase bigfish-purchase'
        idleClassName='iflink-button starter-purchase bigfish-purchase'
        label={metadata.button_label ?? (isStorefrontItemPurchasable(item) ? `购买 ${tier} · ${price}` : undefined)}
      />
    </article>
  )
}

function storefrontCards(mount: StorefrontMount, items: StorefrontCatalogItem[]) {
  const scoped = storefrontItemsForMount(items, mount.kind, mount.zone)
  if (mount.kind === 'plans') {
    return scoped.map((item) => (
      <StorefrontPlanCard key={item.product_id} item={item} />
    ))
  }
  if (mount.kind === 'packs') {
    return scoped.map((item) => (
      <StorefrontPackCard key={item.product_id} item={item} />
    ))
  }
  return scoped.map((item) => (
    <StorefrontZonePackCard key={item.product_id} item={item} />
  ))
}

/**
 * Fills the `data-storefront` mount containers in the custom home HTML with
 * live catalog cards from /api/subscription/public/catalog. Follows the same
 * shadow-DOM portal pattern as LandingModelZones.
 */
export function LandingStorefront(props: {
  containerRef: RefObject<HTMLDivElement | null>
  content: string
}) {
  const [mounts, setMounts] = useState<StorefrontMount[]>([])

  useEffect(() => {
    const shadow = props.containerRef.current?.querySelector(
      '.custom-home-content'
    )?.shadowRoot
    const next = shadow ? readStorefrontMounts(shadow) : []
    for (const mount of next) mount.element.replaceChildren()
    setMounts(next)
  }, [props.containerRef, props.content])

  const catalog = useQuery({
    queryKey: ['storefront-catalog'],
    queryFn: async () => {
      const response = await getStorefrontCatalog()
      if (!response.success) {
        throw new Error(response.message || 'Storefront catalog unavailable')
      }
      return normalizeStorefrontCatalog(response.data)
    },
    enabled: mounts.length > 0,
    retry: false,
    staleTime: 60_000,
  })

  const items = catalog.data ?? []
  return mounts.map((mount, index) =>
    createPortal(
      storefrontCards(mount, items),
      mount.element,
      `${mount.kind}-${mount.zone ?? ''}-${index}`
    )
  )
}
