/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { sideDrawerSwitchItemClassName } from '@/components/drawer-layout'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

// Edit the public presentation fields without discarding metadata owned by
// another editor. This is configuration only, never a purchase permission.
export function ProductDisplayEditor(props: {
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  let metadata: Record<string, unknown> = {}
  try {
    const parsed: unknown = JSON.parse(props.value || '{}')
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      metadata = parsed as Record<string, unknown>
    }
  } catch {
    return (
      <p role='alert'>{t('Product display metadata must be a JSON object')}</p>
    )
  }
  const badges = Array.isArray(metadata.badges) ? metadata.badges : []
  const benefits = Array.isArray(metadata.benefits) ? metadata.benefits : []
  return (
    <div className='flex min-w-0 flex-col gap-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'These fields control product cards. Sales and redemption switches are configured separately.'
        )}
      </p>
      {[
        ['button_label', t('Purchase button label')],
        ['zone', t('Product display zone')],
        ['image', t('Product image URL')],
        ['image_alt', t('Product image description')],
        ['reference_price', t('Reference price label')],
        ['models_display', t('Model display text')],
      ].map(([key, label]) => (
        <div key={key} className='space-y-2'>
          <Label htmlFor={`${id}-${key}`}>{label}</Label>
          <Input
            id={`${id}-${key}`}
            value={typeof metadata[key] === 'string' ? metadata[key] : ''}
            disabled={props.disabled}
            onChange={(event) =>
              props.onChange(
                JSON.stringify({ ...metadata, [key]: event.target.value })
              )
            }
          />
        </div>
      ))}
      <div className='space-y-2'>
        <Label htmlFor={`${id}-badges`}>{t('Badges (one per line)')}</Label>
        <Textarea
          id={`${id}-badges`}
          disabled={props.disabled}
          value={badges
            .map((badge) =>
              typeof badge === 'object' && badge !== null && 'label' in badge
                ? String(badge.label)
                : ''
            )
            .join('\n')}
          onChange={(event) =>
            props.onChange(
              JSON.stringify({
                ...metadata,
                badges: event.target.value.split('\n').map((label, index) => ({
                  ...(typeof badges[index] === 'object' ? badges[index] : {}),
                  label,
                })),
              })
            )
          }
        />
      </div>
      <div className='space-y-2'>
        <Label htmlFor={`${id}-benefits`}>{t('Benefits (one per line)')}</Label>
        <Textarea
          id={`${id}-benefits`}
          disabled={props.disabled}
          value={benefits
            .filter((benefit): benefit is string => typeof benefit === 'string')
            .join('\n')}
          onChange={(event) =>
            props.onChange(
              JSON.stringify({
                ...metadata,
                benefits: event.target.value.split('\n'),
              })
            )
          }
        />
      </div>
      {[
        ['recommended', t('Recommended product')],
        ['featured', t('Featured product')],
        ['preview', t('Show product preview')],
      ].map(([key, label]) => (
        <div key={key} className={sideDrawerSwitchItemClassName()}>
          <Label htmlFor={`${id}-${key}`}>{label}</Label>
          <Switch
            id={`${id}-${key}`}
            disabled={props.disabled}
            checked={metadata[key] === true}
            onCheckedChange={(checked) =>
              props.onChange(JSON.stringify({ ...metadata, [key]: checked }))
            }
          />
        </div>
      ))}
    </div>
  )
}
