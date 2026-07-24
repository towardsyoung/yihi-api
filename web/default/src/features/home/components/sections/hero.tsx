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
import CherryStudio from '@lobehub/icons/es/CherryStudio'
import { Link } from '@tanstack/react-router'
import { ArrowRight, BookOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'

import { HeroTerminalDemo } from '../hero-terminal-demo'

interface HeroProps {
  className?: string
  isAuthenticated?: boolean
}

// Stylized three-dots indicator representing "More"
const MoreIcon = () => (
  <svg
    className='text-muted-foreground/60 group-hover:text-foreground size-6 shrink-0 transition-colors'
    viewBox='0 0 24 24'
    fill='none'
    xmlns='http://www.w3.org/2000/svg'
  >
    <circle cx='6' cy='12' r='2' fill='currentColor' />
    <circle cx='12' cy='12' r='2' fill='currentColor' />
    <circle cx='18' cy='12' r='2' fill='currentColor' />
  </svg>
)

export function Hero(props: HeroProps) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const { systemName, logo } = useSystemConfig()
  const docsUrl =
    (status?.docs_link as string | undefined) || 'https://docs.newapi.pro'

  const renderDocsButton = () => {
    const isExternal = docsUrl.startsWith('http')
    if (isExternal) {
      return (
        <Button
          variant='outline'
          className='group border-border/50 hover:border-border hover:bg-muted/50 inline-flex h-11 items-center gap-1.5 rounded-lg px-5 text-sm font-medium'
          render={
            <a href={docsUrl} target='_blank' rel='noopener noreferrer' />
          }
        >
          <BookOpen className='text-muted-foreground/80 group-hover:text-foreground size-4 transition-colors duration-200' />
          <span>{t('Docs')}</span>
        </Button>
      )
    }
    return (
      <Button
        variant='outline'
        className='group border-border/50 hover:border-border hover:bg-muted/50 inline-flex h-11 items-center gap-1.5 rounded-lg px-5 text-sm font-medium'
        render={<Link to={docsUrl} />}
      >
        <BookOpen className='text-muted-foreground/80 group-hover:text-foreground size-4 transition-colors duration-200' />
        <span>{t('Docs')}</span>
      </Button>
    )
  }

  return (
    <section className='relative z-10 overflow-hidden px-6 pt-28 pb-20 md:pt-36 md:pb-28 lg:pt-40 lg:pb-32'>
      <div
        aria-hidden
        className='pointer-events-none absolute inset-0 -z-10 opacity-30 dark:opacity-20'
        style={{
          background: [
            'radial-gradient(ellipse 60% 55% at 10% 10%, oklch(0.65 0.22 255 / 72%) 0%, transparent 68%)',
            'radial-gradient(ellipse 55% 45% at 92% 22%, oklch(0.78 0.14 205 / 58%) 0%, transparent 70%)',
          ].join(', '),
        }}
      />
      <div
        aria-hidden
        className='absolute inset-0 -z-10 bg-[linear-gradient(to_right,var(--border)_1px,transparent_1px),linear-gradient(to_bottom,var(--border)_1px,transparent_1px)] [mask-image:linear-gradient(to_bottom,black_0%,black_58%,transparent_96%)] bg-[size:3.5rem_3.5rem] opacity-[0.12]'
      />
      <div
        aria-hidden
        className='absolute top-24 left-[7%] -z-10 h-px w-24 bg-gradient-to-r from-blue-500 to-transparent'
      />

      <div className='mx-auto grid max-w-7xl grid-cols-1 items-center gap-14 lg:grid-cols-12 lg:gap-10'>
        <div className='flex flex-col items-start text-left lg:col-span-6'>
          <div
            className='landing-animate-fade-up border-border/50 bg-background/60 mb-7 inline-flex items-center gap-2.5 rounded-full border py-1.5 pr-3.5 pl-1.5 text-xs font-medium opacity-0 shadow-sm backdrop-blur-xl'
            style={{ animationDelay: '0ms' }}
          >
            <img
              src={logo}
              alt=''
              className='size-6 rounded-full object-contain'
              aria-hidden='true'
            />
            <span className='text-foreground break-words'>{systemName}</span>
            <span aria-hidden='true' className='bg-border h-3 w-px' />
            <span className='text-muted-foreground'>
              {t('Unified AI model service')}
            </span>
          </div>

          <h1
            className='landing-animate-fade-up max-w-3xl text-[clamp(3rem,6.6vw,5.7rem)] leading-[0.92] font-semibold tracking-[-0.065em] opacity-0'
            style={{ animationDelay: '60ms' }}
          >
            <span className='text-foreground break-words'>{systemName}</span>
            <span className='mt-4 block bg-gradient-to-r from-blue-600 via-cyan-500 to-blue-500 bg-clip-text text-[0.5em] leading-[1.02] tracking-[-0.04em] text-transparent dark:from-blue-400 dark:via-cyan-300 dark:to-blue-300'>
              {t('One API. Every leading model.')}
            </span>
          </h1>
          <p
            className='landing-animate-fade-up text-muted-foreground mt-7 max-w-xl text-base leading-7 opacity-0 md:text-lg'
            style={{ animationDelay: '120ms' }}
          >
            {t(
              'Access a vast selection of models via a standard, unified API protocol. Power AI applications, manage digital assets, and connect the Future.'
            )}
          </p>

          <div
            className='landing-animate-fade-up mt-8 flex flex-wrap items-center gap-3 opacity-0'
            style={{ animationDelay: '180ms' }}
          >
            {props.isAuthenticated ? (
              <>
                <Button
                  className='group h-11 rounded-lg px-5 text-sm font-medium'
                  render={<Link to='/dashboard' />}
                >
                  {t('Go to Dashboard')}
                  <ArrowRight className='ml-1.5 size-4 transition-transform duration-200 group-hover:translate-x-0.5' />
                </Button>
                {renderDocsButton()}
              </>
            ) : (
              <>
                <Button
                  className='group h-11 rounded-lg px-5 text-sm font-medium'
                  render={<Link to='/sign-up' />}
                >
                  {t('Get Started')}
                  <ArrowRight className='ml-1.5 size-4 transition-transform duration-200 group-hover:translate-x-0.5' />
                </Button>
                <Button
                  variant='outline'
                  className='border-border/50 hover:border-border hover:bg-muted/50 h-11 rounded-lg px-5 text-sm font-medium'
                  render={<Link to='/pricing' />}
                >
                  {t('View Pricing')}
                </Button>
                {renderDocsButton()}
              </>
            )}
          </div>

          <div
            className='landing-animate-fade-up border-border/40 mt-11 w-full max-w-xl border-t pt-6 opacity-0'
            style={{ animationDelay: '240ms' }}
          >
            <div className='mb-4 flex items-end justify-between gap-4'>
              <div>
                <span className='text-muted-foreground/60 text-[10px] font-bold tracking-[0.16em] uppercase'>
                  {t('Supported Applications')}
                </span>
                <p className='text-muted-foreground/70 mt-1 max-w-sm text-xs leading-relaxed'>
                  {t(
                    'Supports one-click configuration and perfectly adapts to NewAPI multi-protocol configuration.'
                  )}
                </p>
              </div>
              <span className='hidden font-mono text-[10px] tracking-[0.14em] text-emerald-600 uppercase sm:block dark:text-emerald-400'>
                {t('AI Application Infrastructure Foundation')}
              </span>
            </div>
            <div className='flex flex-wrap items-center gap-2.5'>
              <a
                href='https://cherry-ai.com'
                target='_blank'
                rel='noopener noreferrer'
                className='group border-border/50 bg-background/55 text-foreground/80 flex items-center gap-2.5 rounded-full border px-4 py-2 text-sm font-medium shadow-sm backdrop-blur-md transition-all duration-300 hover:-translate-y-0.5 hover:border-blue-500/30 hover:bg-blue-500/5'
              >
                <CherryStudio.Color size={21} className='shrink-0' />
                <span>Cherry Studio</span>
              </a>

              <a
                href='https://ccswitch.io'
                target='_blank'
                rel='noopener noreferrer'
                className='group border-border/50 bg-background/55 text-foreground/80 flex items-center gap-2.5 rounded-full border px-4 py-2 text-sm font-medium shadow-sm backdrop-blur-md transition-all duration-300 hover:-translate-y-0.5 hover:border-blue-500/30 hover:bg-blue-500/5'
              >
                <img
                  src='https://ccswitch.io/favicon.png'
                  alt='CC Switch'
                  className='size-[21px] shrink-0 rounded-md object-contain'
                  onError={(event) => {
                    event.currentTarget.style.display = 'none'
                    const fallback = event.currentTarget
                      .nextSibling as HTMLElement | null
                    if (fallback) fallback.style.display = 'flex'
                  }}
                />
                <span
                  style={{ display: 'none' }}
                  className='size-[21px] shrink-0 items-center justify-center rounded-md bg-blue-500/10 text-[9px] font-bold text-blue-600 dark:bg-blue-400/10 dark:text-blue-400'
                >
                  CC
                </span>
                <span>CC Switch</span>
              </a>

              <div className='group border-border/50 bg-background/55 text-muted-foreground flex cursor-default items-center gap-2 rounded-full border px-4 py-2 text-sm font-medium shadow-sm backdrop-blur-md'>
                <MoreIcon />
                <span>{t('More Apps')}</span>
              </div>
            </div>
          </div>
        </div>

        <div
          className='landing-animate-fade-up relative flex w-full flex-col items-center opacity-0 lg:col-span-6'
          style={{ animationDelay: '260ms' }}
        >
          <div
            aria-hidden
            className='absolute -inset-8 -z-10 rounded-[3rem] bg-blue-500/10 blur-3xl dark:bg-cyan-400/5'
          />
          <div className='mb-3 flex w-full max-w-2xl flex-wrap items-center justify-between gap-2 px-1'>
            <div className='flex items-center gap-2 rounded-full border border-white/10 bg-[#08111f] px-3 py-1.5 font-mono text-[10px] tracking-wider text-white/70 shadow-lg'>
              <span className='size-1.5 rounded-full bg-emerald-400 shadow-[0_0_10px_rgba(52,211,153,0.8)]' />
              {t('Secure & Reliable')}
            </div>
            <div className='flex items-center gap-2 rounded-full border border-blue-400/15 bg-[#08111f] px-3 py-1.5 shadow-lg'>
              <span className='font-mono text-[10px] tracking-wider text-cyan-300 uppercase'>
                {t('Model Access')}
              </span>
              <span aria-hidden='true' className='h-3 w-px bg-white/15' />
              <span className='text-[11px] font-medium text-white/80'>
                OpenAI · Claude · Gemini
              </span>
            </div>
          </div>
          <HeroTerminalDemo />
        </div>
      </div>
    </section>
  )
}
