import React from 'react';
import { Avatar, Box, Flex, Heading, Text } from '@radix-ui/themes';
export const Feature = ({
  icon: Icon,
  title,
  description
}) => {
  return /*#__PURE__*/React.createElement(Flex, {
    align: "center",
    gap: "4"
  }, /*#__PURE__*/React.createElement(Avatar, {
    fallback: /*#__PURE__*/React.createElement(Icon, {
      size: 20
    }),
    size: "4"
  }), /*#__PURE__*/React.createElement(Box, null, /*#__PURE__*/React.createElement(Heading, {
    as: "h3",
    size: "5",
    weight: "bold",
    className: "text-[--gray-12]"
  }, title), /*#__PURE__*/React.createElement(Text, {
    size: "3",
    className: "text-[--gray-12]"
  }, description)));
};