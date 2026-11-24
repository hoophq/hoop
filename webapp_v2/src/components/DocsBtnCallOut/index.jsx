import { ArrowUpRight } from 'lucide-react';
import Button from '@/components/Button';

// A link out to the docs, drawn as a button so it shares the app's button
// shape and motion (theme). Two looks: `default` is the secondary button;
// `indigo` is the accented one (light indigo), for a form that leans on the
// docs instead of explaining every field inline.
const VARIANTS = {
  default: 'default',
  indigo: 'light',
};

function DocsBtnCallOut({ text, href, variant = 'default' }) {
  return (
    <Button
      component="a"
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      variant={VARIANTS[variant] ?? VARIANTS.default}
      size="sm"
      leftSection={<ArrowUpRight size={16} aria-hidden="true" />}
    >
      {text}
    </Button>
  );
}

export default DocsBtnCallOut;
