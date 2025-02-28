import React from 'react';
import { Button } from '@radix-ui/themes';
import { ArrowLeft } from 'lucide-react';
import { dispatch } from '../utils/reframe';
export const HeaderBack = () => {
  const handleBack = () => {
    dispatch(['navigate', 'connections']); // Navigate back to connections page
  };
  return /*#__PURE__*/React.createElement(Button, {
    variant: "ghost",
    onClick: handleBack,
    className: "text-gray-500 hover:text-gray-700"
  }, /*#__PURE__*/React.createElement(ArrowLeft, {
    size: 20
  }), /*#__PURE__*/React.createElement("span", null, "Back"));
};