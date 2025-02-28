import React from 'react';
import { Button } from '@radix-ui/themes';
import { ArrowLeft } from 'lucide-react';
import { dispatch } from '../utils/reframe';

export const HeaderBack: React.FC = () => {
  const handleBack = () => {
    dispatch(['navigate', 'connections']); // Navigate back to connections page
  };

  return (
    <Button
      variant="ghost"
      onClick={handleBack}
      className="text-gray-500 hover:text-gray-700"
    >
      <ArrowLeft size={20} />
      <span>Back</span>
    </Button>
  );
}; 
