(ns webapp.components.table)

(defn table-container
  "Container for the table header and content"
  [])

(defn header
  "This component receives one argument that is the component to be rendered
  and a second that is a map of html attributes to be passed to the header"
  [component attrs]
  [:header.bg-gray-100.rounded-t-lg.border-gray-100.border-t.border-r.border-l.text-gray-500.font-bold.px-regular.py-x-small attrs
   component])

(defn rows
  "This component receives one argument that is the component to be rendered
  and a second that is a map of html attributes to be passed to the row container"
  [rows]
  [:ul.border-b.border-l.border-r.border-gray-100
   rows])

(defn row
  "This is the row, to be used inside the table/rows component"
  [item attrs]
  [:li.px-regular.py-small.border-b.border-gray-100.hover:bg-gray-50.transition
   attrs
   item])

;; TODO create a row component to be used across the application
