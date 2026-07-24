/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import Claude from '@lobehub/icons/es/Claude'
import DeepSeek from '@lobehub/icons/es/DeepSeek'
import Gemini from '@lobehub/icons/es/Gemini'
import Minimax from '@lobehub/icons/es/Minimax'
import OpenAI from '@lobehub/icons/es/OpenAI'
import Qwen from '@lobehub/icons/es/Qwen'
import { useRef, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'

const MODEL_PROVIDERS = [
  {
    name: 'OpenAI',
    icon: <OpenAI size={27} />,
    tags: ['Reasoning', 'Multimodal'],
  },
  {
    name: 'Claude',
    icon: <Claude.Color size={27} />,
    tags: ['Code', 'Reasoning'],
  },
  {
    name: 'Gemini',
    icon: <Gemini.Color size={27} />,
    tags: ['Multimodal', 'Vision'],
  },
  {
    name: 'DeepSeek',
    icon: <DeepSeek.Color size={27} />,
    tags: ['Reasoning', 'Code'],
  },
  {
    name: 'Qwen',
    icon: <Qwen.Color size={27} />,
    tags: ['Model Access', 'Multimodal'],
  },
  {
    name: 'MiniMax',
    icon: <Minimax.Color size={27} />,
    tags: ['Code', 'Model Access'],
  },
] as const

interface CounterProps {
  end: number
  suffix?: string
  prefix?: string
  duration?: number
  decimals?: number
}

function Counter(props: CounterProps) {
  const { end, suffix = '', prefix = '', duration = 1600, decimals = 0 } = props
  const ref = useRef<HTMLSpanElement>(null)
  const startedRef = useRef(false)

  const formatValue = useCallback(
    (v: number) =>
      decimals > 0 ? v.toFixed(decimals) : Math.round(v).toLocaleString(),
    [decimals]
  )

  const animate = useCallback(() => {
    const el = ref.current
    if (!el) return
    const start = performance.now()
    const step = (now: number) => {
      const progress = Math.min((now - start) / duration, 1)
      const eased = 1 - Math.pow(1 - progress, 3)
      el.textContent = `${prefix}${formatValue(eased * end)}${suffix}`
      if (progress < 1) requestAnimationFrame(step)
    }
    requestAnimationFrame(step)
  }, [end, duration, prefix, suffix, formatValue])

  useEffect(() => {
    const el = ref.current
    if (!el) return

    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    if (mq.matches) {
      el.textContent = `${prefix}${formatValue(end)}${suffix}`
      return
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting && !startedRef.current) {
          startedRef.current = true
          animate()
          observer.unobserve(el)
        }
      },
      { threshold: 0.5 }
    )

    observer.observe(el)
    return () => observer.disconnect()
  }, [animate, end, prefix, suffix, formatValue])

  return (
    <span ref={ref} className='tabular-nums'>
      {prefix}0{suffix}
    </span>
  )
}

interface StatsProps {
  className?: string
}

interface StatItem {
  end: number
  suffix: string
  label: string
  decimals?: number
}

export function Stats(_props: StatsProps) {
  const { t } = useTranslation()

  const stats: StatItem[] = [
    { end: 50, suffix: '+', label: t('upstream services integrated') },
    { end: 100, suffix: '+', label: t('model billing support') },
    { end: 50, suffix: '+', label: t('compatible API routes') },
    { end: 10, suffix: '+', label: t('scheduling controls') },
  ]

  return (
    <section className='relative z-10 px-6 pb-10 md:pb-16'>
      <AnimateInView
        animation='scale-in'
        className='relative mx-auto max-w-7xl overflow-hidden rounded-[2rem] border border-white/10 bg-[#08111f] px-6 py-9 text-white shadow-[0_32px_90px_-42px_rgba(3,16,35,0.9)] md:px-10 md:py-12'
      >
        <div
          aria-hidden
          className='absolute inset-0 bg-[radial-gradient(circle_at_0%_0%,rgba(37,99,235,0.28),transparent_35%),radial-gradient(circle_at_100%_20%,rgba(34,211,238,0.14),transparent_30%)]'
        />
        <div
          aria-hidden
          className='absolute inset-0 bg-[linear-gradient(to_right,rgba(255,255,255,0.04)_1px,transparent_1px),linear-gradient(to_bottom,rgba(255,255,255,0.04)_1px,transparent_1px)] [mask-image:linear-gradient(to_bottom,black,transparent)] bg-[size:3rem_3rem]'
        />

        <div className='relative'>
          <div className='flex flex-col justify-between gap-5 md:flex-row md:items-end'>
            <div className='max-w-xl'>
              <p className='mb-3 font-mono text-[11px] font-medium tracking-[0.18em] text-cyan-300 uppercase'>
                {t('Model Access')}
              </p>
              <h2 className='text-2xl leading-tight font-semibold tracking-tight md:text-4xl'>
                {t('Leading models, one consistent API')}
              </h2>
            </div>
            <p className='max-w-md text-sm leading-6 text-slate-300'>
              {t(
                'Choose the right model for every workload without rebuilding your integration.'
              )}
            </p>
          </div>

          <div className='mt-9 grid gap-2 sm:grid-cols-2 lg:grid-cols-3'>
            {MODEL_PROVIDERS.map((provider) => (
              <div
                key={provider.name}
                className='group flex items-center gap-3.5 rounded-xl border border-white/[0.08] bg-white/[0.04] px-4 py-3.5 transition-all duration-300 hover:-translate-y-0.5 hover:border-cyan-300/25 hover:bg-white/[0.07]'
              >
                <div className='flex size-10 shrink-0 items-center justify-center rounded-lg bg-white/[0.07] text-white'>
                  {provider.icon}
                </div>
                <div className='min-w-0'>
                  <h3 className='text-sm font-semibold text-white'>
                    {provider.name}
                  </h3>
                  <p className='mt-1 truncate text-[11px] text-slate-400'>
                    {provider.tags.map((tag) => t(tag)).join(' · ')}
                  </p>
                </div>
                <span className='ml-auto size-1.5 shrink-0 rounded-full bg-emerald-400 shadow-[0_0_8px_rgba(52,211,153,0.65)]' />
              </div>
            ))}
          </div>

          <div className='mt-9 grid grid-cols-2 border-t border-white/10 pt-8 md:grid-cols-4'>
            {stats.map((stat, index) => (
              <div
                key={stat.label}
                className={`flex flex-col px-2 text-center md:px-6 ${
                  index % 2 === 1 ? 'border-l border-white/10' : ''
                } ${index > 0 ? 'md:border-l md:border-white/10' : ''}`}
              >
                <span className='text-2xl font-semibold tracking-tight text-white md:text-3xl'>
                  <Counter
                    end={stat.end}
                    suffix={stat.suffix}
                    decimals={stat.decimals}
                  />
                </span>
                <span className='mt-1.5 text-[11px] leading-4 text-slate-400 md:text-xs'>
                  {stat.label}
                </span>
              </div>
            ))}
          </div>
        </div>
      </AnimateInView>
    </section>
  )
}
