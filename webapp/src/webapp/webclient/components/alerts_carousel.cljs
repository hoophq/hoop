(ns webapp.webclient.components.alerts-carousel
  (:require
   ["@radix-ui/themes" :refer [Box Callout Flex Text IconButton]]
   ["lucide-react" :refer [X ArrowUpRight]]))

(defn alert-item [{:keys [color icon text title action-text on-action link-href single? closeable on-close]}]
  [:> Box {:class (if single?
                    "w-full"
                    "flex-shrink-0 min-w-[200px] max-w-[220px] min-h-[96px] last:pr-3")}
   [:> Callout.Root {:color (or color "yellow")
                     :role "alert"
                     :size "1"
                     :class "h-full relative"}
    ;; Close button
    (when (and closeable on-close)
      [:> IconButton {:size "2"
                      :variant "ghost"
                      :class (str "absolute top-3 right-3 z-10 "
                                  (when (= color "yellow") "text-warning-12"))
                      :onClick on-close}
       [:> X {:size 14}]])

    (when icon
      [:> Callout.Icon icon])



    [:> Callout.Text {:size "1" :class (str
                                        (when (= color "yellow") "text-warning-12 ")
                                        (when closeable "pr-8"))}
     [:> Text {:as "p" :size "1" :weight "bold"
               :class (str (when (= color "yellow") "text-warning-12 "))}
      title]
     text]
    (when (or action-text link-href)
      [:> Flex {:align "center" :gap "1" :class "self-end"}
       (when action-text
         [:> Callout.Text {:size "1" :weight "bold"
                           :class (str (when (= color "yellow") "text-warning-12 ")
                                       "cursor-pointer flex gap-1")
                           :onClick on-action}
          action-text
          [:> ArrowUpRight {:size 16}]])])]])

(defn alerts-carousel
  "Simple horizontal scrolling alerts component.
   Props:
   - alerts: vector of alert maps with keys:
     {:id :color :icon :text :action-text :on-action :link-href
      :closeable :on-close}"
  [{:keys [alerts]}]
  (when (seq alerts)
    ;; Container com scroll apenas quando há múltiplos alertas
    [:> Box {:class (if (> (count alerts) 1) "overflow-x-auto" "")
             :style (when (> (count alerts) 1)
                      {:scrollbar-width "thin"
                       :scrollbar-color "#94a3b8 transparent"})}
     [:> Flex {:gap "3" :class "p-3"}
      (for [alert alerts]
        ^{:key (:id alert)}
        [alert-item (assoc alert :single? (= (count alerts) 1))])]]))

(defn main [props]
  [alerts-carousel props])
