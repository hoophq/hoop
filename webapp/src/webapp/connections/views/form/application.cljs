(ns webapp.connections.views.form.application
  (:require ["@headlessui/react" :as ui]
            ["unique-names-generator" :as ung]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.headings :as h]
            [webapp.connections.constants :as constants]
            [webapp.connections.views.form.hoop-run-instructions :as instructions]
            [webapp.connections.views.form.toggle-data-masking :as toggle-data-masking]
            [webapp.connections.views.form.toggle-review :as toggle-review]
            [webapp.shared-ui.sidebar.connection-overlay :as connection-overlay]
            [webapp.components.multiselect :as multi-select]))

(defn random-connection-name []
  (let [numberDictionary (.generate ung/NumberDictionary #js{:length 4})
        characterName (ung/uniqueNamesGenerator #js{:dictionaries #js[ung/animals ung/starWars]
                                                    :style "lowerCase"
                                                    :length 1})]
    (str characterName "-" numberDictionary)))

(defn js-select-options->list [options]
  (mapv #(get % "value") options))

(defn main []
  (let [user (rf/subscribe [:users->current-user])]
    (fn [{:keys [connection-name
                 connection-type
                 connection-subtype
                 connection-command
                 tags-value
                 tags-input-value
                 user-groups
                 form-type
                 api-key
                 review-toggle-enabled?
                 approval-groups-value
                 data-masking-toggle-enabled?
                 data-masking-groups-value]}]
      [:<>
       [:section {:class "mb-large"}
        [:div {:class "mb-small"}
         [h/h4-md "Choose your stack"]
         [:label {:class "text-xs text-gray-500"}
          "Check our supported stacks "
          [:a {:class "text-blue-500"
               :href (str "https://hoop.dev/docs/connections")
               :target "_blank"}
           "here"]]]
        [:> ui/RadioGroup {:value @connection-subtype
                           :disabled (= form-type :update)
                           :onChange (fn [type]
                                       (reset! connection-subtype type)
                                       (reset! connection-type :application)
                                       (reset! connection-name (str type "-" (random-connection-name)))
                                       (reset! connection-command (get constants/connection-commands type)))}
         [:> (.-Label ui/RadioGroup) {:className "sr-only"}
          "Applications connections"]
         [:div {:class "space-y-2"}
          (for [application [{:type "ruby-on-rails" :label "Ruby on Rails"}
                             {:type "python" :label "Python"}
                             {:type "nodejs" :label "Node JS"}
                             {:type "clojure" :label "Clojure"}]]
            ^{:key (:type application)}
            [:> (.-Option ui/RadioGroup)
             {:value (:type application)
              :className (fn [params]
                           (str "relative flex cursor-pointer flex-col rounded-lg border p-4 focus:outline-none md:grid md:grid-cols-3 md:pl-4 md:pr-6 "
                                (if (.-checked params)
                                  "z-10 bg-gray-900"
                                  "border-gray-200")))}
             (fn [params]
               (r/as-element
                [:<>
                 [:span {:class "flex items-center text-sm"}
                  [:span {:aria-hidden "true"
                          :class (str "h-4 w-4 rounded-full border bg-white flex items-center justify-center "
                                      (if (.-checked params)
                                        "border-transparent"
                                        "border-gray-300")
                                      (when (.-active params)
                                        "ring-2 ring-offset-2 ring-indigo-600 "))}
                   [:span {:class (str "rounded-full w-1.5 h-1.5 "
                                       (if (.-checked params)
                                         "bg-gray-900"
                                         "bg-white"))}]]
                  [:> (.-Label ui/RadioGroup) {:as "span"
                                               :className (str "ml-3 font-medium "
                                                               (if (.-checked params)
                                                                 "text-white"
                                                                 "text-gray-700"))}
                   (:label application)]]]))])]]]

       [:section {:class "mb-large"}
        [toggle-review/main {:free-license? (:free-license? (:data @user))
                             :user-groups user-groups
                             :review-toggle-enabled? review-toggle-enabled?
                             :approval-groups-value approval-groups-value}]

        [toggle-data-masking/main {:free-license? (:free-license? (:data @user))
                                   :data-masking-toggle-enabled? data-masking-toggle-enabled?
                                   :data-masking-groups-value data-masking-groups-value}]
        [multi-select/text-input {:value tags-value
                                  :input-value tags-input-value
                                  :disabled? false
                                  :required? false
                                  :on-change (fn [value]
                                               (reset! tags-value value))
                                  :on-input-change (fn [value]
                                                     (reset! tags-input-value value))
                                  :label "Tags"
                                  :label-description "Categorize your connections with specific identifiers"
                                  :id "tags-multi-select-text-input"
                                  :name "tags-multi-select-text-input"}]]

       [:section {:class "mb-large"}
        [:div {:class "mb-large"}
         [instructions/install-hoop]]

        [:div {:class "mb-large"}
         [instructions/setup-token @api-key]]

        [:div {:class "mb-large"}
         [instructions/run-hoop-connection {:connection-name @connection-name
                                            :connection-subtype @connection-subtype
                                            :review? @review-toggle-enabled?
                                            :review-groups (js-select-options->list @approval-groups-value)
                                            :data-masking? @data-masking-toggle-enabled?
                                            :data-masking-fields (js-select-options->list @data-masking-groups-value)}]]]

       [:div {:class "flex justify-end items-center"}
        [:span {:class "text-gray-400 text-sm whitespace-pre block"}
         "If you have finished the setup, you can "]
        [:span {:class "text-blue-500 text-sm cursor-pointer"
                :on-click (fn []
                            (rf/dispatch [:connections->get-connections])
                            (reset! connection-overlay/overlay-open? true))}
         "check your connections."]]])))
