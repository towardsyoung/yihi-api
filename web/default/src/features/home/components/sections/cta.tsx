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
import { Link } from '@tanstack/react-router'
import { ArrowRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'
import { Button } from '@/components/ui/button'
import { useSystemConfig } from '@/hooks/use-system-config'

interface CTAProps {
  className?: string
  isAuthenticated?: boolean
}

export function CTA(props: CTAProps) {
  const { t } = useTranslation()
  const { systemName, logo } = useSystemConfig()

  if (props.isAuthenticated) {
    return null
  }

  return (
    <section className='landing-deferred-section relative z-10 overflow-hidden px-6 py-24 md:py-32'>
      <AnimateInView
        className='relative mx-auto max-w-7xl overflow-hidden rounded-[2rem] bg-blue-600 px-6 py-16 text-center text-white shadow-[0_32px_90px_-42px_rgba(37,99,235,0.8)] md:px-12 md:py-20 dark:bg-blue-700'
        animation='scale-in'
      >
        <div
          aria-hidden
          className='absolute inset-0 bg-[radial-gradient(circle_at_10%_10%,rgba(255,255,255,0.2),transparent_32%),radial-gradient(circle_at_90%_90%,rgba(34,211,238,0.24),transparent_34%)]'
        />
        <div
          aria-hidden
          className='absolute inset-0 bg-[linear-gradient(to_right,rgba(255,255,255,0.08)_1px,transparent_1px),linear-gradient(to_bottom,rgba(255,255,255,0.08)_1px,transparent_1px)] [mask-image:radial-gradient(circle_at_center,black,transparent_78%)] bg-[size:3.5rem_3.5rem]'
        />
        <div className='relative'>
          <div className='mb-6 flex items-center justify-center gap-2.5'>
            <img
              src={logo}
              alt=''
              aria-hidden='true'
              className='size-8 rounded-lg bg-white/10 object-contain'
            />
            <span className='text-sm font-semibold tracking-tight'>
              {systemName}
            </span>
          </div>
          <h2 className='text-3xl leading-tight font-semibold tracking-[-0.04em] md:text-5xl'>
            {t('Ready to simplify')}
            <br />
            <span className='text-cyan-100'>{t('your AI integration?')}</span>
          </h2>
          <p className='mx-auto mt-5 max-w-lg text-sm leading-6 text-blue-100 md:text-base'>
            {t(
              'Deploy your own gateway and start routing requests through your configured upstream services.'
            )}
          </p>
          <div className='mt-8 flex flex-wrap items-center justify-center gap-3'>
            <Button
              className='group bg-white text-blue-700 hover:bg-blue-50'
              render={<Link to='/sign-up' />}
            >
              {t('Get Started')}
              <ArrowRight className='ml-1 size-3.5 transition-transform duration-200 group-hover:translate-x-0.5' />
            </Button>
            <Button
              variant='outline'
              className='border-white/30 bg-transparent text-white hover:bg-white/10 hover:text-white'
              render={<Link to='/pricing' />}
            >
              {t('View Pricing')}
            </Button>
          </div>
        </div>
      </AnimateInView>
    </section>
  )
}
