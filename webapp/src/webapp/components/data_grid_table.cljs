(ns webapp.components.data-grid-table
  (:require [reagent.core :as r]
            [webapp.config :as config]))

(def dark-theme
  {"component"                  "#15202b"

       ;; bottombar & topbar
   "button"                     "#2f3b47"
   "button-icon"                "#8899a6"
   "input"                      "#15202b"
   "input|text"                 "#ffffff"
   "input|border"               "#000000"
   "input:focus|border"         "#0078ff"
   "input-info"                 "#8899a6"

       ;; contextmenu
   "contextmenu"                "#15202b"
   "contextmenu|text"           "#ffffff"
   "contextmenu-item:highlight" "#192734"
   "contextmenu-item-shortcut"  "#8899a6"


   "blanksheet"                 "#15202b"
   "sheet"                      "#192734"
   "sheet|text"                 "#ffffff"

       ;; scrollbar
   "scrollbar"                  "#000000"
   "scrollbar|border"           "#444444"

       ;; grid lines
   "gridline"                   "#ffffff"
   "gridline-tip"               "#ffffff"
   "gridline|opacity"           0.10

       ;; headers
   "header"                     "#15202b"
   "header|text"                "#ffffff"
   "header:highlight"           "#192734"
   "header:selected"            "#2a4157"
   "header:selected|text"       "#ffffff"
   "header-icon"                "#ecf5f4"

       ;; action ranges
   "cellrange:cut"              "#0078ff"
   "cellrange:copy"             "#0078ff"
   "cellrange:fill"             "#ffffff"

       ;; selection
   "cellcursor"                 "#0078ff"
   "cellrange:selected"         "#0078ff"
   "cellrange:selected|border"  "#0078ff"
   "cellrange:selected|opacity" 0.10

       ;; fill handle
   "fillhandle"                 "#0078ff"

       ;; cell editor
   "celleditor"                 "#0078ff"

       ;; search
   "searchcursor"               "#ecf5f4"
   "cell+found"                 "#abc5dc"
   "cell+found|opacity"         0.25

       ;; freezeline
   "freezeline"                 "#2a4157"
   "freezeline-tip"             "#2f5478"
   "freezelineplaceholder"      "#2f5478"

       ;; move action
   "move?ghost"                 "#ffffff"
   "move?ghost|opacity"         0.1
   "move?guide"                 "#8899a6"
   "move?guide|opacity"         0.5

       ;; freeze action
   "freeze?hint"                 "#0078ff"
   "freeze?ghost"                "#ffffff"
   "freeze?ghost|opacity"        0.1
   "freeze?guide"                "#8899a6"
   "freeze?guide|opacity"        0.5

       ;; resize action
   "resize?hint"                 "#0078ff"
   "resize?guide"                "#0078ff"
   "resize?guide|opacity"        1

       ;; show action
   "show?hint"                   "#15202b"
   "show?hint-icon"              "#ffffff"
   "show?hint|border"            "#0078ff"})

(defn- data-grid
  "head -> is an array of strings where each string corresponds to each table title.
  body -> is an array matrix where each array corresponds to one line of the table"
  [head body dark-theme? allow-copy?]
  (let [!ref (atom {:current nil})
        head-formatted (map-indexed (fn [idx value] {:title value :source idx}) head)]
    (fn
      []
      (r/create-class
       {:display-name "data-grid-table"
        :component-did-mount
        (fn []
          (swap! !ref assoc :current (new js/DataGridXL (:current @!ref) (clj->js {:data body
                                                                                   :columns head-formatted
                                                                                   :fontFamily "Sora"
                                                                                   :theme (clj->js (if dark-theme?
                                                                                                     dark-theme
                                                                                                     {}))
                                                                                   :allowEditCells false
                                                                                   :instantActivate false
                                                                                   :allowCopy allow-copy?
                                                                                   :bottomBar []}))))
        :reagent-render
        (fn []
          [:div {:class "w-full h-full"
                 :ref (fn [el]
                        (swap! !ref assoc :current el))}])}))))

(defn main [head body dark-theme? loading? allow-copy?]
  (if loading?
    [:div {:class "flex justify-center items-center h-full"}
     [:figure.w-4
      [:img.animate-spin {:src (str config/webapp-url "/icons/icon-loader-circle-white.svg")}]]]
    [data-grid head body dark-theme? allow-copy?]))
