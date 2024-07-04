(ns webapp.components.typography)

(defn default
  [text attrs]
  [:span.text-gray-900 attrs text])

(defn small
  [text attrs]
  [:small.text-gray-700 attrs text])
