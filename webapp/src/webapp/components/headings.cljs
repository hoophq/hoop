(ns webapp.components.headings)

(defn h1
  "<h1> html tag"
  [text attrs]
  [:h1.text-4xl.font-bold.text-gray-900
   attrs
   text])

(defn h2
  "<h2> html tag"
  [text attrs]
  [:h2.text-2xl.font-bold.text-gray-900
   attrs
   text])

(defn h3
  [text attrs]
  [:h3.text-xl.font-bold.text-gray-900
   attrs
   text])

(defn h4
  [text attrs]
  [:h4.text-l.font-bold.text-gray-900
   attrs
   text])

(defn h4-md
  [text attrs]
  [:h4.text-base.text-gray-900
   attrs
   text])
