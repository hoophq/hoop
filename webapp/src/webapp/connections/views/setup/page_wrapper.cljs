(ns webapp.connections.views.setup.page-wrapper
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [webapp.connections.views.setup.footer :as footer]))

(defn main [{:keys [children footer-props]}]
  ;; Fixed-height internal scroll container so the footer flows at the true end of
  ;; the content instead of pinning to the viewport (EVL-103). min-h-full + grow
  ;; keep the footer at the bottom on short pages; shrink-0 keeps tall content from
  ;; being clipped (the container scrolls instead). Content + footer always fit the
  ;; viewport, so the footer never spills below the fold.
  ;; `relative` is required, not cosmetic: it makes this box the containing block
  ;; for absolutely positioned descendants (e.g. react-select's off-screen a11y
  ;; text). Without it they resolve against an ancestor, escape this scroll
  ;; container and extend the document, producing a second scrollbar that drags
  ;; the in-flow footer into the middle of the viewport. The Radix ScrollArea this
  ;; replaced provided the same guarantee via its relatively positioned viewport.
  [:> Box {:class "relative h-screen overflow-y-auto"}
   [:> Box {:class "min-h-full flex flex-col"}
    ;; bg-gray-1 here so the fill area (below short content) keeps the page
    ;; background instead of exposing the app shell behind it — pages no longer
    ;; need their own min-h-screen to fill the viewport. flex flex-col lets a page
    ;; opt into filling this area (e.g. a centered onboarding screen) with flex-1.
    [:> Box {:class "grow shrink-0 bg-gray-1 flex flex-col"}
     children]

    ;; Footer with Delete button when in update mode
    (let [base-footer-props (dissoc footer-props :on-delete)]
     [footer/main
      (assoc
       (if (:on-delete footer-props)
         (assoc base-footer-props
                :middle-button {:variant "ghost"
                                :color "red"
                                :text "Delete"
                                :on-click (:on-delete footer-props)})
         base-footer-props)
       :static? true)])]])
