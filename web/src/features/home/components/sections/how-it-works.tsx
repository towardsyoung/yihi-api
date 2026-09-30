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
import { Settings, Zap, BarChart3 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'

export function HowItWorks() {
  const { t } = useTranslation()

  const steps = [
    {
      num: '1',
      title: t('Configure'),
      desc: t(
        'Add your API keys, set up channels and configure access permissions'
      ),
      icon: <Settings className='size-6' strokeWidth={1.5} />,
    },
    {
      num: '2',
      title: t('Connect'),
      desc: t(
        'Connect through OpenAI, Claude, Gemini, and other compatible API routes'
      ),
      icon: <Zap className='size-6' strokeWidth={1.5} />,
    },
    {
      num: '3',
      title: t('Monitor'),
      desc: t('Track usage, costs and performance with real-time analytics'),
      icon: <BarChart3 className='size-6' strokeWidth={1.5} />,
    },
  ]

  return (
    <section className='landing-deferred-section border-border/40 bg-muted/10 relative z-10 border-y px-6 py-24 md:py-32'>
      <div className='mx-auto max-w-7xl'>
        <AnimateInView className='mb-16 text-center md:mb-20'>
          <p className='mb-3 font-mono text-[11px] font-medium tracking-[0.18em] text-blue-600 uppercase dark:text-blue-400'>
            {t('How It Works')}
          </p>
          <h2 className='text-3xl font-semibold tracking-[-0.035em] md:text-5xl'>
            {t('Three steps to get started')}
          </h2>
        </AnimateInView>

        <div className='relative grid gap-4 md:grid-cols-3'>
          <div
            aria-hidden
            className='bg-border absolute top-8 right-[17%] left-[17%] hidden h-px md:block'
          />
          {steps.map((step, i) => (
            <AnimateInView
              key={step.num}
              delay={i * 150}
              animation='fade-up'
              className='bg-background border-border/50 relative flex flex-col rounded-2xl border p-7 shadow-sm md:p-8'
            >
              <div className='mb-10 flex items-center justify-between'>
                <div className='text-muted-foreground border-border/50 bg-muted/30 flex size-14 items-center justify-center rounded-2xl border'>
                  {step.icon}
                </div>
                <span className='font-mono text-4xl font-light text-blue-500/25'>
                  0{step.num}
                </span>
              </div>
              <h3 className='mb-2 text-lg font-semibold'>{step.title}</h3>
              <p className='text-muted-foreground text-sm leading-6'>
                {step.desc}
              </p>
            </AnimateInView>
          ))}
        </div>
      </div>
    </section>
  )
}
