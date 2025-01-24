(ns webapp.connections.views.setup.stepper
  (:require
   ["@radix-ui/themes" :refer [Badge Box Flex Separator Text]]
   [re-frame.core :as rf]))

(def steps
  [{:id :type
    :number 1
    :title "Resource"}
   {:id :database-credentials
    :number 2
    :title "Credentials"}
   {:id :additional-config
    :number 3
    :title "Review and Create"}])

(defn- step-number [{:keys [number active? completed?]}]
  [:> Badge
   {:size "1"
    :radius "full"
    :variant "soft"
    :color (if active?
             "indigo"
             "gray")}
   [:> Text {:size "1" :weight "bold" :class (cond
                                               completed? "text-[--gray-a11]"
                                               active? "text-[--indigo-a11]"
                                               :else "text-[--gray-a11] opacity-50")}
    number]])

(defn- step-title [{:keys [title active? completed?]}]
  [:> Text
   {:size "2"
    :weight "bold"
    :class (cond
             completed? "text-[--gray-a11]"
             active? "text-[--indigo-a11]"
             :else "text-[--gray-a11] opacity-50")}
   title])

(defn main []
  (let [current-step (rf/subscribe [:connection-setup/current-step])]
    (fn []
      (println @current-step)
      [:> Flex {:align "center" :justify "center" :class "mb-8"}
       (doall
        (for [{:keys [id number title]} steps]
          ^{:key id}
          [:> Flex {:align "center"}
        ;; Step container (number + title)
           [:> Flex {:align "center" :gap "2"}
            [step-number {:number number
                          :active? (= id @current-step)
                          :completed? (or (= id :resource)
                                          (and (= @current-step :additional-config)
                                               (= id :credentials)))}]
            [step-title {:title title
                         :active? (= id @current-step)
                         :completed? (or (= id :resource)
                                         (and (= @current-step :additional-config)
                                              (= id :credentials)))}]]

        ;; Separator (except for last item)
           (when-not (= id :additional-config)
             [:> Box {:class "px-2"}
              [:> Separator
               {:size "1"
                :orientation "horizontal"
                :class "w-4"}]])]))])))
