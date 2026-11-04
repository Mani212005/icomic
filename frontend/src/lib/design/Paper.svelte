<svg class="paper-wash" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1440 700" preserveAspectRatio="none" aria-hidden="true" focusable="false">
  <defs>
    <filter id="wash-edge" x="-30%" y="-30%" width="160%" height="160%">
      <feTurbulence type="fractalNoise" baseFrequency=".012 .02" numOctaves="3" seed="8" result="noise" />
      <feDisplacementMap in="SourceGraphic" in2="noise" scale="38" />
      <feGaussianBlur stdDeviation="5" />
    </filter>
    <radialGradient id="ochre"><stop stop-color="#dba75e" stop-opacity=".30"/><stop offset="1" stop-color="#dba75e" stop-opacity="0"/></radialGradient>
    <radialGradient id="sage"><stop stop-color="#7b9c8c" stop-opacity=".28"/><stop offset="1" stop-color="#7b9c8c" stop-opacity="0"/></radialGradient>
  </defs>
  <g filter="url(#wash-edge)">
    <ellipse cx="40" cy="200" rx="450" ry="270" fill="url(#ochre)"/>
    <ellipse cx="1410" cy="350" rx="500" ry="340" fill="url(#sage)"/>
    <ellipse cx="1100" cy="50" rx="350" ry="150" fill="url(#ochre)"/>
  </g>
</svg>
<style>
  .paper-wash { position: absolute; inset: 0 0 auto; width: 100%; height: 850px; pointer-events: none; z-index: -1; }
</style>
