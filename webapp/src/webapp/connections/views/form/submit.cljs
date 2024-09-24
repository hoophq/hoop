(ns webapp.connections.views.form.submit
  (:require ["@heroicons/react/20/solid" :as hero-solid-icon]
            [clojure.string :as cs]
            [webapp.components.button :as button]))

(defmulti btn-submit-label identity)
(defmethod btn-submit-label :create [_ agent]
  (str "Save for " agent))
(defmethod btn-submit-label :create-hoop-run [_ agent]
  (str "Save for " agent))
(defmethod btn-submit-label :update [_ agent]
  (str "Update for " agent))

(defn main [form-type current-agent-name current-agent-id agents]
  (let [current-agent (first (filter (fn [{:keys [id]}] (= id @current-agent-id)) (:data agents)))
        agent-options (map (fn [{:keys [id name status]}] {:value id
                                                           :text (if (= (cs/upper-case status) "DISCONNECTED")
                                                                   (str name " (" status ")")
                                                                   name)}) (:data agents))
        current-name (if (= :create-onboarding form-type)
                       (:text (first agent-options))
                       @current-agent-name)]
    [:<>
     [:div {:class "flex justify-end gap-regular"}
      (if (<= (count agent-options) 1)
        [button/primary {:text "Save"
                         :disabled (< (count agent-options) 1)
                         :type "submit"}]
        [button/primary {:text [btn-submit-label form-type current-name]
                         :more-options (map #(:text %) agent-options)
                         :on-click-option (fn [agent]
                                            (reset! current-agent-name agent)
                                            (reset! current-agent-id
                                                    (:value (first (filter #(= (:text %) agent) agent-options)))))
                         :type "submit"}])]
     (when (and (= form-type :update)
                (= (:status current-agent) "DISCONNECTED"))
       [:div {:class "flex justify-end items-center gap-small mt-2"}
        [:> hero-solid-icon/ExclamationTriangleIcon {:class "h-6 w-6 shrink-0 text-red-300"
                                                     :aria-hidden "true"}]
        [:small {:class "text-red-300"}
         "The hoop selected is not connected."]])

     (when (< (count agent-options) 1)
       [:div {:class "mt-6 py-3 px-4 rounded-md flex items-center justify-center bg-gray-100 border border-gray-300"}
        [:span {:class "text-sm text-gray-500"}
         "No Agents found. "
         [:a {:class "text-blue-600 underline"
              :href "https://hoop.dev/docs/concepts/agent"
              :target "_blank"}
          "Click here"]

         " to learn how to setup one before creating a connection."]
        [:> hero-solid-icon/ArrowUpRightIcon {:class "h-4 w-4 text-gray-600 ml-3"}]])]))
