(ns webapp.jira-templates.create-update-form
  (:require
   [re-frame.core :as rf]
   [reagent.core :as r]
   ["@radix-ui/themes" :refer [Box Grid Flex Heading Badge]]
   [webapp.components.loaders :as loaders]
   [webapp.jira-templates.helpers :as helpers]
   [webapp.jira-templates.form-header :as form-header]
   [webapp.jira-templates.basic-info :as basic-info]
   [webapp.jira-templates.rules-table :as rules-table]))

(defn jira-form [form-type template scroll-pos]
  (let [state (helpers/create-form-state template)
        handlers (helpers/create-form-handlers state)]
    (fn []
      [:> Box {:class "min-h-screen bg-gray-1"}
       [:form {:id "jira-form"
               :on-submit (fn [e]
                            (.preventDefault e)
                            (let [data {:id @(:id state)
                                        :name @(:name state)
                                        :description @(:description state)
                                        :jira_template @(:jira_template state)}]
                              (if (= :edit form-type)
                                (rf/dispatch [:jira-templates->update-by-id data])
                                (rf/dispatch [:jira-templates->create data]))))}

        [form-header/main
         {:form-type form-type
          :id @(:id state)
          :scroll-pos scroll-pos}]

        [:> Box {:p "7" :class "space-y-radix-9"}
         [basic-info/main
          {:name (:name state)
           :description (:description state)
           :on-name-change #(reset! (:name state) %)
           :on-description-change #(reset! (:description state) %)}]

         [:> Grid {:columns "7" :gap "7"}
          [:> Box {:grid-column "span 2 / span 2"}
           [:> Flex {:align "center" :gap "2"}
            [:> Heading {:as "h3" :size "4" :weight "medium"} "Configure rules"]]
           [:p.text-sm.text-gray-500.mb-4
            "Gorem ipsum dolor sit amet, consectetur adipiscing elit."]]

          [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
           [rules-table/main
            (merge
             {:title "Integration details"
              :state (:jira_template state)
              :select-state (:select-state state)}
             handlers)]]]]]])))

(defn- loading []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn main [form-type]
  (let [jira-template (rf/subscribe [:jira-templates->active-template])
        scroll-pos (r/atom 0)]
    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))]
        (.addEventListener js/window "scroll" handle-scroll)
        (finally
          (.removeEventListener js/window "scroll" handle-scroll)))

      (if (= :loading (:status @jira-template))
        [loading]
        [jira-form form-type (:data @jira-template) scroll-pos]))))
