(ns webapp.onboarding.resource-providers
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Button Card Flex Heading Text]]
   ["lucide-react" :refer [ChevronRight]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.config :as config]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]))

(def provider-options
  [{:id "aws"
    :icon (r/as-element
           [:figure {:class "w-6"}
            [:img {:role "aws-icon"
                   :src (str config/webapp-url "/icons/automatic-resources/aws.svg")}]])
    :title "Amazon Web Services"
    :description "Access AWS to retrieve and connect your database resources."
    :badge nil
    :action #(do
               (rf/dispatch [:aws-connect/initialize-state])
               (rf/dispatch [:connection-setup/set-type :aws-connect])
               (rf/dispatch [:navigate :onboarding-aws-connect]))}
   {:id "mongodb-atlas"
    :icon (r/as-element
           [:figure {:class "w-6"}
            [:img {:role "mongodb-atlas-icon"
                   :src (str config/webapp-url "/icons/automatic-resources/mongodb.svg")}]])
    :title "MongoDB Atlas"
    :description "Access your resources on Atlas Database."
    :badge "SOON"
    :action nil}])

(defn provider-card [{:keys [icon title description badge action]}]
  [:> Card {:size "1"
            :variant "surface"
            :class (str "w-full cursor-pointer hover:before:bg-primary-12 group "
                        (when badge "opacity-60 pointer-events-none"))
            :on-click (when-not badge action)}
   [:> Flex {:align "center" :justify "between" :class "group-hover:text-[--gray-1]"}
    [:> Flex {:align "center" :gap "3"}
     [:> Avatar {:size "4"
                 :class "group-hover:bg-[--white-a3]"
                 :variant "soft"
                 :color "gray"
                 :fallback icon}]
     [:> Flex {:direction "column"}
      [:> Flex {:align "center" :gap "2"}
       [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
       (when badge
         [:> Box {:class "text-xs font-medium px-2 py-0.5 rounded-full bg-gray-200 text-gray-500"}
          badge])]
      [:> Text {:size "2" :color "gray-11"} description]]]
    [:> ChevronRight {:size 18 :class "text-gray-6 group-hover:text-[--gray-1]"}]]])

(defn resource-providers-content []
  [:> Flex {:direction "column" :align "center" :justify "center" :class "h-screen"}
   [:> Box {:class "absolute top-0 right-0 p-radix-5"}
    [:> Button {:variant "ghost"
                :size "2"
                :color "gray"
                :on-click #(rf/dispatch [:auth->logout])}
     "Logout"]]

   [:> Box {:class "space-y-radix-7 w-[600px]"}
    [:> Box {:class "space-y-radix-6"}
     [:> Box
      [:img {:src (str config/webapp-url "/images/hoop-branding/PNG/hoop-symbol_black@4x.png")
             :class "w-16 mx-auto py-4"}]]

     ;; Title
     [:> Box
      [:> Heading {:as "h1" :align "center" :size "6" :mb "2" :weight "bold" :class "text-[--gray-12]"}
       "What type of resource are you connecting to?"]
      [:> Text {:as "p" :size "3" :align "center" :class "text-[--gray-12]"}
       "Choose the service provider for your resources:"]]

     ;; Cards
     [:> Box {:class "space-y-radix-4 max-w-[600px]"}
      (for [option provider-options]
        ^{:key (:id option)}
        [provider-card option])]]]])

(defn main []
  (r/create-class
   {:component-did-mount
    (fn [])

    :reagent-render
    (fn []
      [page-wrapper/main
       {:children [resource-providers-content]
        :footer-props
        {:form-type :onboarding
         :back-text "Back"
         :on-back #(rf/dispatch [:navigate :onboarding-setup])
         :next-text nil
         :next-disabled? true
         :next-hidden? true
         :back-hidden? false}}])}))
